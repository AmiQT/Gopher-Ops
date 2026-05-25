package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/health"
	"gopher-ops/pkg/monitor"
	"gopher-ops/pkg/notify"
	"gopher-ops/pkg/ratelimit"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

// version is populated from the Go build's embedded VCS info. It surfaces in
// /status so operators always know which commit they're talking to.
var version = func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var commit, when string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			commit = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if commit == "" {
		return "dev"
	}
	short := commit
	if len(short) > 7 {
		short = short[:7]
	}
	if modified {
		short += "-dirty"
	}
	if when != "" {
		return fmt.Sprintf("%s (%s)", short, when)
	}
	return short
}()

// safeSend wraps bot.Send so a failed delivery is logged instead of silently
// vanishing. Returns the Message and the underlying error so callers that
// need the message ID can still use it.
func safeSend(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable) (tgbotapi.Message, error) {
	sent, err := bot.Send(msg)
	if err != nil {
		slog.Warn("telegram send failed", "error", err)
	}
	return sent, err
}

const (
	stateFile       = "state.json"
	defaultRCALines = 200
	rcaCacheTTL     = 1 * time.Hour
)

// markdownEscaper neutralizes Telegram Markdown specials in untrusted strings
// (container names, log payloads) so a crafted log line cannot break rendering
// or smuggle formatting into operator alerts.
var markdownEscaper = strings.NewReplacer(
	"_", "\\_",
	"*", "\\*",
	"`", "\\`",
	"[", "\\[",
)

func safeMD(s string) string { return markdownEscaper.Replace(s) }

// atomicWriteFile writes via tmp + rename so a crash mid-write cannot leave a
// half-written state.json that would corrupt the next boot.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Clean(path))
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("🤖 Booting up Gopher-Ops SRE Telegram Bot...", "version", version)

	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found — credentials must be set via environment")
	}

	// Critical: must run AFTER godotenv.Load() so .env values apply.
	ai.LoadEnvConfig()

	if p := os.Getenv("AUDIT_LOG_PATH"); p != "" {
		audit.SetPath(p)
	}
	var auditMaxBytes int64 = 10 * 1024 * 1024
	if v := os.Getenv("AUDIT_LOG_MAX_SIZE_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			auditMaxBytes = n * 1024 * 1024
		}
	}
	auditMaxFiles := 5
	if v := os.Getenv("AUDIT_LOG_MAX_FILES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			auditMaxFiles = n
		}
	}
	audit.SetRotation(auditMaxBytes, auditMaxFiles)
	audit.Log(audit.Event{Type: "boot", Actor: "system", Status: "ok"})

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		slog.Error("GEMINI_API_KEY is not set")
		os.Exit(1)
	}

	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tgToken == "" {
		slog.Error("TELEGRAM_BOT_TOKEN is not set")
		os.Exit(1)
	}

	authorizedIDs, authorizedSet, err := parseAuthorizedChatIDs()
	if err != nil {
		slog.Error("Authorized chat IDs invalid", "error", err)
		os.Exit(1)
	}
	if len(authorizedIDs) == 0 {
		slog.Error("AUTHORIZED_CHAT_IDS (or AUTHORIZED_CHAT_ID) is not set — required for production safety")
		os.Exit(1)
	}
	slog.Info("Authorized operators configured", "count", len(authorizedIDs))

	// Configurable thresholds
	cpuThreshold := 80.0
	if v := os.Getenv("ALERT_CPU_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cpuThreshold = f
		}
	}

	cpuDuration := 5
	if v := os.Getenv("ALERT_CPU_DURATION"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cpuDuration = n
		}
	}

	if v := os.Getenv("METRICS_HISTORY_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			monitor.SetMetricsHistorySize(n)
		}
	}

	alertCooldown := 15 * time.Minute
	if v := os.Getenv("ALERT_COOLDOWN_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			alertCooldown = time.Duration(n) * time.Minute
		}
	}

	rateLimit := 30.0
	if v := os.Getenv("RATE_LIMIT_PER_MINUTE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			rateLimit = f
		}
	}
	limiter := ratelimit.New(rateLimit)

	// 1. Initialize AI Agent
	llmProvider := os.Getenv("LLM_PROVIDER")
	if llmProvider == "" {
		llmProvider = "gemini"
	}

	aiCfg := ai.Config{
		Provider:  llmProvider,
		APIKey:    apiKey,
		BaseURL:   os.Getenv("LLM_BASE_URL"),
		ModelName: os.Getenv("LLM_MODEL"),
	}

	if llmProvider == "local" {
		if localKey := os.Getenv("LOCAL_LLM_API_KEY"); localKey != "" {
			aiCfg.APIKey = localKey
		} else {
			aiCfg.APIKey = "lm-studio"
		}
	}

	agent, err := ai.NewAgent(aiCfg)
	if err != nil {
		slog.Error("Failed to initialize AI agent", "provider", llmProvider, "error", err)
		os.Exit(1)
	}
	defer agent.Close()

	// 2. Initialize Telegram Bot
	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		slog.Error("Failed to initialize Telegram bot", "error", err)
		os.Exit(1)
	}
	bot.Debug = false
	slog.Info("✅ Authorized on Telegram", "username", bot.Self.UserName)

	// 3. Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Optional /health HTTP endpoint
	if port := os.Getenv("HEALTH_PORT"); port != "" {
		health.New(port).Start(ctx)
	}

	// 5. Async Telegram broadcaster (fan-out to all authorized operators)
	queueSize := 256
	if v := os.Getenv("TELEGRAM_QUEUE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			queueSize = n
		}
	}
	broadcaster := notify.NewQueue(bot, authorizedIDs, queueSize)
	go broadcaster.Run(ctx)

	// 6. Reset channel — lets /reset signal the monitor goroutine to flush
	// its in-memory trackers (not just delete state.json).
	resetCh := make(chan struct{}, 1)

	// 7. Start Background Monitor with panic recovery and auto-restart
	go func() {
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("🔥 Background monitor panicked — restarting", "panic", r)
					}
				}()
				startBackgroundMonitor(ctx, broadcaster, agent, resetCh, cpuThreshold, cpuDuration, alertCooldown)
			}()

			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				slog.Warn("Background monitor restarting after crash...")
			}
		}
	}()

	// 5. Telegram update loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			slog.Info("🛑 Shutdown signal received — stopping bot")
			audit.Log(audit.Event{Type: "shutdown", Actor: "system", Status: "ok"})
			bot.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.CallbackQuery != nil {
				chatID := update.CallbackQuery.Message.Chat.ID
				if !authorizedSet[chatID] {
					callbackConfig := tgbotapi.NewCallback(update.CallbackQuery.ID, "❌ Kau siapa siot?")
					bot.Request(callbackConfig)
					continue
				}
				if !limiter.Allow(chatID) {
					callbackConfig := tgbotapi.NewCallback(update.CallbackQuery.ID, "⏳ Slow down — rate limited.")
					bot.Request(callbackConfig)
					continue
				}
				handleCallbackQuery(bot, agent, update.CallbackQuery)
				continue
			}

			if update.Message == nil {
				continue
			}

			chatID := update.Message.Chat.ID
			if !authorizedSet[chatID] {
				msg := tgbotapi.NewMessage(chatID, "❌ Warganegara asing dilarang masuk. Kau sapa takde access level nak borak dengan aku bro. GG.")
				safeSend(bot, msg)
				audit.Log(audit.Event{
					Type: "alert", Actor: "system", Action: "unauthorized_access",
					Target: strconv.FormatInt(chatID, 10), Status: "info",
				})
				continue
			}

			if !limiter.Allow(chatID) {
				msg := tgbotapi.NewMessage(chatID, "⏳ Whoa, slow down bro! Limit kau dah cecah. Try semula dalam seminit.")
				safeSend(bot, msg)
				continue
			}

			if update.Message.IsCommand() {
				msgText := ""
				switch update.Message.Command() {
				case "start":
					msgText = "🤖 **Gopher-Ops Ready!**\nSebut je apa kau nak aku buat kat server ni mat."
				case "help":
					msgText = "📖 **COMMANDS:**\n/start - Mula sembang\n/help - Tengok ni\n/status - Check bot & AI status\n/reset - Clear state"
				case "status":
					health, _ := monitor.GetSystemHealth()
					provider := os.Getenv("LLM_PROVIDER")
					model := os.Getenv("LLM_MODEL")
					autoPilot := "DISABLED ❌"
					if os.Getenv("AUTOPILOT_ENABLED") == "true" {
						autoPilot = "ACTIVE ✅ (Self-Healing On)"
					}
					msgText = fmt.Sprintf("🤖 **GOPHER-OPS STATUS**\n\n"+
						"🔖 **Version:** %s\n"+
						"🧠 **AI Engine:** %s (%s)\n"+
						"🤖 **Autopilot:** %s\n"+
						"👥 **Operators:** %d configured\n\n"+
						"📊 **Live Metrics:**\n%s",
						version, strings.ToUpper(provider), model, autoPilot, len(authorizedIDs), health)
				case "reset":
					os.Remove(stateFile)
					select {
					case resetCh <- struct{}{}:
					default: // already pending — fine
					}
					audit.Log(audit.Event{Type: "action", Actor: "user", Action: "reset_state", Status: "ok"})
					msgText = "🧹 **STATE CLEARED!** Hafiz dah lupa sejarah lama. Kita mula hidup baru."
				default:
					msgText = "Command apa tu mat? Aku tak faham ah. 😂"
				}
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, msgText)
				msg.ParseMode = "Markdown"
				safeSend(bot, msg)
				continue
			}

			input := strings.TrimSpace(update.Message.Text)
			if input == "clear" {
				agent.ResetSession()
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "🧹 Gopher-Ops: Chat memory cleared! Token history reset jadi 0. Jimat token LFG! ✨")
				msg.ParseMode = "Markdown"
				safeSend(bot, msg)
				continue
			}

			msgLoader := tgbotapi.NewMessage(update.Message.Chat.ID, "🧠 Gopher-Ops is thinking...")
			sentLoader, _ := safeSend(bot, msgLoader)

			response, intents, err := agent.ProcessRequest(input)
			if err != nil {
				slog.Error("AI ProcessRequest failed", "error", err)
				respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, fmt.Sprintf("❌ Error borak: %v", err))
				respMsg.ParseMode = "Markdown"
				safeSend(bot, respMsg)
				continue
			}

			respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, response)
			respMsg.ParseMode = "Markdown"

			if len(intents) > 0 {
				var rows [][]tgbotapi.InlineKeyboardButton
				for _, intent := range intents {
					callbackData := fmt.Sprintf("%s:%s", intent.Action, intent.Target)
					btnText := ""
					targetName := intent.Target
					if intent.Target != "" {
						foundName := monitor.GetContainerName(intent.Target)
						if foundName != "" && foundName != intent.Target {
							shortID := intent.Target
							if len(shortID) > 8 {
								shortID = shortID[:8]
							}
							targetName = fmt.Sprintf("%s (%s)", foundName, shortID)
						}
					}

					switch intent.Action {
					case "StopContainer":
						btnText = "🛑 Stop " + targetName
					case "StartContainer":
						btnText = "🟢 Start " + targetName
					case "RestartContainer":
						btnText = "🔄 Restart " + targetName
					case "ClearCache":
						btnText = "🧹 Clear OS Cache"
					case "ViewLogs":
						btnText = "📜 View Logs " + targetName
					case "TerraformApply":
						btnText = "🏗️ Run Terraform Apply"
					case "AuditSecurity":
						btnText = "🛡️ Audit Security Level"
					case "VisualMetrics":
						btnText = "📊 Show System Pulse"
					case "ContainerStats":
						btnText = "📦 Live Container Stats"
					case "InvestigateNetwork":
						btnText = "🌐 Triage: Network " + targetName
					case "CheckConfig":
						btnText = "⚙️ Triage: Config " + targetName
					}

					btn := tgbotapi.NewInlineKeyboardButtonData(btnText, callbackData)
					rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
				}
				keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
				respMsg.ReplyMarkup = &keyboard
			}

			safeSend(bot, respMsg)
		}
	}
}

// handleCallbackQuery processes button clicks from the user in Telegram.
func handleCallbackQuery(bot *tgbotapi.BotAPI, agent ai.AIAgent, callback *tgbotapi.CallbackQuery) {
	callbackConfig := tgbotapi.NewCallback(callback.ID, "🧠 Gopher-Ops is processing...")
	bot.Request(callbackConfig)

	parts := strings.Split(callback.Data, ":")
	if len(parts) != 2 {
		safeSend(bot, tgbotapi.NewMessage(callback.Message.Chat.ID, "❌ Callback data rosak bro."))
		return
	}

	action := parts[0]
	target := parts[1]

	auditAction := func(act, tgt string, err error) {
		status, detail := "ok", ""
		if err != nil {
			status, detail = "error", err.Error()
		}
		audit.Log(audit.Event{Type: "action", Actor: "user", Action: act, Target: tgt, Status: status, Detail: detail})
	}

	var resMsg string
	switch action {
	case "StopContainer":
		_, err := actions.StopContainer(target)
		auditAction(action, target, err)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh stop %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ Mantap! Container %s dah ditutup bro 💀", target)
		}
	case "StartContainer":
		_, err := actions.StartContainer(target)
		auditAction(action, target, err)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh start %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ LFG! Container %s dah hidup balik 🔥", target)
		}
	case "RestartContainer":
		_, err := actions.RestartContainer(target)
		auditAction(action, target, err)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh restart %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ Selesai! Container %s freshly restarted ✨", target)
		}
	case "ClearCache":
		res := actions.ClearCache()
		auditAction(action, "", nil)
		resMsg = fmt.Sprintf("✅ Selesai! %s 🧹", res)
	case "ViewLogs":
		logs, err := actions.GetContainerLogs(target)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh tarik log %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("📜 **Logs for %s:**\n```\n%s\n```", safeMD(target), logs)
		}
	case "AnalyzeRCA":
		name := monitor.GetContainerName(target)

		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "🔍 *Gopher-Ops sedang menganalisis log... Sabar jap.*")
		msgProc.ParseMode = "Markdown"
		safeSend(bot, msgProc)

		evidence := buildRCAEvidence(target)
		analysis, err := agent.DiagnoseIssue(name, evidence)
		auditAction("AnalyzeRCA", target, err)
		if err != nil {
			resMsg = fmt.Sprintf("❌ Aduh, AI pening nak analyze: %v", err)
		} else {
			resMsg = fmt.Sprintf("🧐 **ANALISIS RCA: %s**\n\n%s", safeMD(name), analysis)
		}
	case "TerraformApply":
		out, err := actions.TerraformApply()
		if err != nil {
			resMsg = fmt.Sprintf("❌ **Terraform Failed!**\n```\n%s\n```", out)
		} else {
			resMsg = fmt.Sprintf("✅ **Terraform Success!**\n```\n%s\n```", out)
		}
	case "AuditSecurity":
		data, _ := monitor.GetSecurityContext()
		imageScanData, _ := monitor.GetImageSecurityReport()

		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "🛡️ *Gopher-Ops sedang melakukan audit keselamatan...*")
		msgProc.ParseMode = "Markdown"
		safeSend(bot, msgProc)

		analysis, err := agent.AuditSecurity(data, imageScanData)
		if err != nil {
			resMsg = fmt.Sprintf("❌ Aduh, AI gagal buat audit: %v", err)
		} else {
			resMsg = fmt.Sprintf("🛡️ **SECURITY AUDIT REPORT**\n\n%s", analysis)
		}
	case "InvestigateNetwork":
		data := actions.InvestigateNetwork(target)
		name := monitor.GetContainerName(target)

		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "🌐 *Gopher-Ops sedang memeriksa rangkaian...*")
		msgProc.ParseMode = "Markdown"
		safeSend(bot, msgProc)

		analysis, err := agent.TriageIssue(name, "Network Connectivity", data)
		if err != nil {
			resMsg = fmt.Sprintf("❌ AI pening nak check network: %v", err)
		} else {
			resMsg = fmt.Sprintf("🌐 **ANALISIS RANGKAIAN: %s**\n\n%s", name, analysis)
		}
	case "CheckConfig":
		data := actions.CheckConfig(target)
		name := monitor.GetContainerName(target)

		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "⚙️ *Gopher-Ops sedang memeriksa konfigurasi...*")
		msgProc.ParseMode = "Markdown"
		safeSend(bot, msgProc)

		analysis, err := agent.TriageIssue(name, "Configuration Validation", data)
		if err != nil {
			resMsg = fmt.Sprintf("❌ AI pening nak check config: %v", err)
		} else {
			resMsg = fmt.Sprintf("⚙️ **ANALISIS KONFIGURASI: %s**\n\n%s", name, analysis)
		}
	case "VisualMetrics":
		metrics, _ := monitor.GetVisualMetrics()
		resMsg = metrics
	case "ContainerStats":
		resMsg = monitor.FormatContainerStats()
	default:
		resMsg = "Action tak jumpa wtf ahaha"
	}

	msg := tgbotapi.NewMessage(callback.Message.Chat.ID, resMsg)
	safeSend(bot, msg)
}

// buildRCAEvidence assembles the inspect post-mortem, system metric snapshot,
// and a deep log tail into a single text bundle for the AI. This is what
// upgrades RCA from "guess from 10 log lines" to "reason about exit codes,
// OOM flags, restart history, system load, and 200 lines of context."
func buildRCAEvidence(containerID string) string {
	rcaLines := defaultRCALines
	if v := os.Getenv("RCA_LOG_LINES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rcaLines = n
		}
	}

	var sb strings.Builder

	if crash, err := monitor.GetCrashContext(containerID); err == nil {
		sb.WriteString(crash.FormatForAI())
		sb.WriteString("\n")
	} else {
		sb.WriteString("=== CONTAINER POST-MORTEM ===\n(inspect unavailable: " + err.Error() + ")\n\n")
	}

	sb.WriteString(monitor.GetMetricsSnapshot(15))
	sb.WriteString("\n")

	logs, err := actions.GetContainerLogsN(containerID, rcaLines)
	if err != nil {
		sb.WriteString("=== CONTAINER LOGS ===\n(unavailable: " + err.Error() + ")\n")
	} else {
		sb.WriteString(fmt.Sprintf("=== CONTAINER LOGS (last %d lines) ===\n%s\n", rcaLines, logs))
	}

	return sb.String()
}

// startBackgroundMonitor checks system health every minute and broadcasts
// alerts to every authorized operator via the async notify queue. The
// broadcaster decouples this loop from Telegram API latency, so a slow API
// cannot stall container-state polling.
//
// `resetCh` is a signal channel that lets the /reset command flush the
// in-memory trackers — without it, /reset only deletes state.json while the
// goroutine keeps stale restart counters and cooldowns in memory.
func startBackgroundMonitor(ctx context.Context, broadcaster *notify.Queue, agent ai.AIAgent, resetCh <-chan struct{}, cpuThreshold float64, cpuDuration int, alertCooldown time.Duration) {
	slog.Info("🩺 Background monitor started",
		"cpu_threshold", cpuThreshold,
		"cpu_duration_min", cpuDuration,
		"alert_cooldown", alertCooldown)

	previousStates, err := monitor.GetContainerStates()
	if err != nil {
		slog.Warn("Monitor init error — starting with empty state", "error", err)
		previousStates = make(map[string]monitor.ContainerStatus)
	}

	restartTracker := make(map[string]int)
	alertState := make(map[string]string)
	alertSentAt := make(map[string]time.Time)

	var mu sync.Mutex
	saveState := func() {
		mu.Lock()
		defer mu.Unlock()

		state := struct {
			Restarts    map[string]int       `json:"restarts"`
			Alerts      map[string]string    `json:"alerts"`
			AlertSentAt map[string]time.Time `json:"alert_sent_at"`
		}{
			Restarts:    restartTracker,
			Alerts:      alertState,
			AlertSentAt: alertSentAt,
		}
		data, _ := json.Marshal(state)
		if err := atomicWriteFile(stateFile, data, 0644); err != nil {
			slog.Warn("state.json write failed", "error", err)
		}
	}

	if file, err := os.ReadFile(stateFile); err == nil {
		var state struct {
			Restarts    map[string]int       `json:"restarts"`
			Alerts      map[string]string    `json:"alerts"`
			AlertSentAt map[string]time.Time `json:"alert_sent_at"`
		}
		if err := json.Unmarshal(file, &state); err == nil {
			if state.Restarts != nil {
				restartTracker = state.Restarts
			}
			if state.Alerts != nil {
				alertState = state.Alerts
			}
			if state.AlertSentAt != nil {
				alertSentAt = state.AlertSentAt
			}
			slog.Info("💾 State loaded from state.json")
		} else {
			slog.Warn("state.json corrupt — starting fresh", "error", err)
		}
	}

	rcaCache := make(map[string]struct {
		Result    string
		Timestamp time.Time
	})

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Background monitor stopping")
			return
		case <-resetCh:
			slog.Info("🧹 Monitor state reset via /reset command")
			restartTracker = make(map[string]int)
			alertState = make(map[string]string)
			alertSentAt = make(map[string]time.Time)
			for k := range rcaCache {
				delete(rcaCache, k)
			}
			audit.Log(audit.Event{Type: "action", Actor: "system", Action: "monitor_reset", Status: "ok"})
			continue
		case <-ticker.C:
		}

		// Collect per-container CPU/RAM in the background; errors are non-fatal.
		go func() {
			collectCtx, collectCancel := context.WithTimeout(ctx, 30*time.Second)
			defer collectCancel()
			if err := monitor.CollectContainerStats(collectCtx); err != nil {
				slog.Warn("container stats collection failed", "error", err)
			}
		}()

		currentStates, err := monitor.GetContainerStates()
		if err != nil {
			slog.Warn("Monitor loop error", "error", err)
			continue
		}

		// Evict RCA cache entries that are stale OR refer to containers that
		// no longer exist. Without this, the cache grows unboundedly.
		for k, v := range rcaCache {
			if time.Since(v.Timestamp) > rcaCacheTTL {
				delete(rcaCache, k)
				continue
			}
			if _, alive := currentStates[k]; !alive {
				delete(rcaCache, k)
			}
		}

		autoPilot := os.Getenv("AUTOPILOT_ENABLED") == "true"

		for id, current := range currentStates {
			prev, exists := previousStates[id]
			safeName := safeMD(current.Name)

			if exists && prev.State == "running" && current.State != "running" {
				count := restartTracker[id]

				if autoPilot && count < 3 {
					restartTracker[id]++
					slog.Info("🤖 Auto-Pilot restart", "attempt", restartTracker[id], "container", current.Name, "id", id)
					audit.Log(audit.Event{
						Type: "autopilot", Actor: "ai", Action: "RestartContainer",
						Target: id, Status: "info",
						Detail: fmt.Sprintf("attempt %d/3 for %s", restartTracker[id], current.Name),
					})

					var analysis string
					if cache, ok := rcaCache[id]; ok && time.Since(cache.Timestamp) < 10*time.Minute {
						analysis = cache.Result + "\n\n*(Nota: Analisis dari cache 10 minit terakhir)*"
					} else {
						evidence := buildRCAEvidence(id)
						var diagErr error
						analysis, diagErr = agent.DiagnoseIssue(current.Name, evidence)
						if diagErr != nil {
							slog.Warn("DiagnoseIssue failed during autopilot", "container", current.Name, "error", diagErr)
							analysis = "*(Analisis AI tidak tersedia: " + safeMD(diagErr.Error()) + ")*"
						}
						rcaCache[id] = struct {
							Result    string
							Timestamp time.Time
						}{Result: analysis, Timestamp: time.Now()}
					}

					if _, restartErr := actions.RestartContainer(id); restartErr != nil {
						slog.Warn("Autopilot restart failed", "container", current.Name, "error", restartErr)
						audit.Log(audit.Event{
							Type: "autopilot", Actor: "ai", Action: "RestartContainer",
							Target: id, Status: "error", Detail: restartErr.Error(),
						})
					}

					msgText := fmt.Sprintf("🤖 **AUTOPILOT ACTION! (Attempt %d/3)**\n\nContainer **%s** tadi DOWN. Aku dah tolong restartkan untuk kau!\n\n**Analisis AI:**\n%s",
						restartTracker[id], safeName, analysis)
					broadcaster.Broadcast(notify.Broadcast{Text: msgText, ParseMode: "Markdown"})
					alertState[id] = "autopilot_sent"
					alertSentAt[id] = time.Now()
					saveState()
				} else if shouldAlert(alertState[id], alertSentAt[id], alertCooldown) {
					alertMsg := ""
					if count >= 3 {
						alertMsg = fmt.Sprintf("🚨 **CRITICAL ALERT!**\n\nContainer **%s** dah restart 3 kali tapi still DOWN mat. 💀 Aku dah 'surrender'. Kau kena check manual jap bro.", safeName)
					} else {
						alertMsg = fmt.Sprintf("⚠️ **ALERT!** Container **%s** dah **DOWN**! (Status: %s)\nNak aku restart ke bro?", safeName, safeMD(current.State))
					}
					alertState[id] = "alert_sent"
					alertSentAt[id] = time.Now()
					audit.Log(audit.Event{
						Type: "alert", Actor: "system", Action: "container_down",
						Target: id, Status: "info", Detail: current.Name,
					})

					btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+id)
					btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Siasat Punca (RCA)", "AnalyzeRCA:"+id)
					keyboard := tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(btnRestart),
						tgbotapi.NewInlineKeyboardRow(btnRCA),
					)
					broadcaster.Broadcast(notify.Broadcast{
						Text: alertMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard,
					})
					saveState()
				}
			}

			if current.State == "running" {
				if restartTracker[id] > 0 || alertState[id] != "" {
					slog.Info("✨ Container healthy again — resetting trackers", "container", current.Name)
					restartTracker[id] = 0
					alertState[id] = ""
					saveState()
				}
			}
		}

		// HTTP endpoint probing
		if urlsStr := os.Getenv("MONITOR_URLS"); urlsStr != "" {
			for _, u := range strings.Split(urlsStr, ",") {
				u = strings.TrimSpace(u)
				status := monitor.CheckHTTP(u)
				if !status.IsUp {
					alertMsg := fmt.Sprintf("🌐 **HTTP ALERT!** Website [%s](%s) return error!\nStatus Code: **%d**\nCheck jap bro, kot-kot app hang.", u, u, status.StatusCode)
					broadcaster.Broadcast(notify.Broadcast{Text: alertMsg, ParseMode: "Markdown"})
				}
			}
		}

		// Sustained CPU load alert
		if monitor.GlobalMetricStore.CheckSustainedLoad(cpuThreshold, cpuDuration) {
			alertMsg := fmt.Sprintf("🔥 **SUSTAINED HIGH LOAD ALERT!**\n\nCPU load kau dah lebih **%.0f%%** untuk %d minit berturut-turut bro! Server tengah berpeluh tu. 🥵\n\nNak aku check container mana yang makan resource paling banyak?", cpuThreshold, cpuDuration)
			btnCheck := tgbotapi.NewInlineKeyboardButtonData("📊 Check Resource Hogs", "VisualMetrics:all")
			keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnCheck))
			broadcaster.Broadcast(notify.Broadcast{
				Text: alertMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard,
			})
		}

		previousStates = currentStates
	}
}

// shouldAlert returns true when a fresh alert for `id` is allowed. It rate
// limits repeat alerts so a flapping container cannot spam the operator.
//   - first failure (status == ""): always alert
//   - same failure within cooldown: suppress
//   - same failure after cooldown elapsed: re-alert
func shouldAlert(status string, lastSent time.Time, cooldown time.Duration) bool {
	if status == "" {
		return true
	}
	if lastSent.IsZero() {
		return true
	}
	return time.Since(lastSent) > cooldown
}

// parseAuthorizedChatIDs accepts a comma-separated AUTHORIZED_CHAT_IDS env
// (preferred), or a single AUTHORIZED_CHAT_ID for backwards compatibility,
// and returns the list plus a set for O(1) lookup.
func parseAuthorizedChatIDs() ([]int64, map[int64]bool, error) {
	raw := os.Getenv("AUTHORIZED_CHAT_IDS")
	if raw == "" {
		raw = os.Getenv("AUTHORIZED_CHAT_ID")
	}
	if raw == "" {
		return nil, nil, nil
	}

	var ids []int64
	set := make(map[int64]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid chat ID %q: %w", part, err)
		}
		if !set[id] {
			set[id] = true
			ids = append(ids, id)
		}
	}
	return ids, set, nil
}
