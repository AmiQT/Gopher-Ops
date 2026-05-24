package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/monitor"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("🤖 Booting up Gopher-Ops SRE Telegram Bot...")

	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found — credentials must be set via environment")
	}

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

	authIDStr := os.Getenv("AUTHORIZED_CHAT_ID")
	if authIDStr == "" {
		slog.Error("AUTHORIZED_CHAT_ID is not set — required for production safety")
		os.Exit(1)
	}
	authorizedChatID, err := strconv.ParseInt(authIDStr, 10, 64)
	if err != nil {
		slog.Error("AUTHORIZED_CHAT_ID must be a valid number", "error", err)
		os.Exit(1)
	}

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

	// 4. Start Background Monitor with panic recovery and auto-restart
	go func() {
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("🔥 Background monitor panicked — restarting", "panic", r)
					}
				}()
				startBackgroundMonitor(ctx, bot, agent, authorizedChatID, cpuThreshold, cpuDuration)
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
			bot.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.CallbackQuery != nil {
				if update.CallbackQuery.Message.Chat.ID != authorizedChatID {
					callbackConfig := tgbotapi.NewCallback(update.CallbackQuery.ID, "❌ Kau siapa siot?")
					bot.Request(callbackConfig)
					continue
				}
				handleCallbackQuery(bot, agent, update.CallbackQuery)
				continue
			}

			if update.Message == nil {
				continue
			}

			if update.Message.Chat.ID != authorizedChatID {
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "❌ Warganegara asing dilarang masuk. Kau sapa takde access level nak borak dengan aku bro. GG.")
				bot.Send(msg)
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
						"🧠 **AI Engine:** %s (%s)\n"+
						"🤖 **Autopilot:** %s\n\n"+
						"📊 **Live Metrics:**\n%s",
						strings.ToUpper(provider), model, autoPilot, health)
				case "reset":
					os.Remove("state.json")
					msgText = "🧹 **STATE CLEARED!** Hafiz dah lupa sejarah lama. Kita mula hidup baru."
				default:
					msgText = "Command apa tu mat? Aku tak faham ah. 😂"
				}
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, msgText)
				msg.ParseMode = "Markdown"
				bot.Send(msg)
				continue
			}

			input := strings.TrimSpace(update.Message.Text)
			if input == "clear" {
				agent.ResetSession()
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "🧹 Gopher-Ops: Chat memory cleared! Token history reset jadi 0. Jimat token LFG! ✨")
				msg.ParseMode = "Markdown"
				bot.Send(msg)
				continue
			}

			msgLoader := tgbotapi.NewMessage(update.Message.Chat.ID, "🧠 Gopher-Ops is thinking...")
			sentLoader, _ := bot.Send(msgLoader)

			response, intents, err := agent.ProcessRequest(input)
			if err != nil {
				slog.Error("AI ProcessRequest failed", "error", err)
				respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, fmt.Sprintf("❌ Error borak: %v", err))
				respMsg.ParseMode = "Markdown"
				bot.Send(respMsg)
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

			bot.Send(respMsg)
		}
	}
}

// handleCallbackQuery processes button clicks from the user in Telegram.
func handleCallbackQuery(bot *tgbotapi.BotAPI, agent ai.AIAgent, callback *tgbotapi.CallbackQuery) {
	callbackConfig := tgbotapi.NewCallback(callback.ID, "🧠 Gopher-Ops is processing...")
	bot.Request(callbackConfig)

	parts := strings.Split(callback.Data, ":")
	if len(parts) != 2 {
		bot.Send(tgbotapi.NewMessage(callback.Message.Chat.ID, "❌ Callback data rosak bro."))
		return
	}

	action := parts[0]
	target := parts[1]

	var resMsg string
	switch action {
	case "StopContainer":
		_, err := actions.StopContainer(target)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh stop %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ Mantap! Container %s dah ditutup bro 💀", target)
		}
	case "StartContainer":
		_, err := actions.StartContainer(target)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh start %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ LFG! Container %s dah hidup balik 🔥", target)
		}
	case "RestartContainer":
		_, err := actions.RestartContainer(target)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh restart %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("✅ Selesai! Container %s freshly restarted ✨", target)
		}
	case "ClearCache":
		res := actions.ClearCache()
		resMsg = fmt.Sprintf("✅ Selesai! %s 🧹", res)
	case "ViewLogs":
		logs, err := actions.GetContainerLogs(target)
		if err != nil {
			resMsg = fmt.Sprintf("❌ GG! Takleh tarik log %s: %v", target, err)
		} else {
			resMsg = fmt.Sprintf("📜 **Logs for %s:**\n```\n%s\n```", target, logs)
		}
	case "AnalyzeRCA":
		logs, _ := actions.GetContainerLogs(target)
		name := monitor.GetContainerName(target)

		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "🔍 *Gopher-Ops sedang menganalisis log... Sabar jap.*")
		msgProc.ParseMode = "Markdown"
		bot.Send(msgProc)

		analysis, err := agent.DiagnoseIssue(name, logs)
		if err != nil {
			resMsg = fmt.Sprintf("❌ Aduh, AI pening nak analyze: %v", err)
		} else {
			resMsg = fmt.Sprintf("🧐 **ANALISIS RCA: %s**\n\n%s", name, analysis)
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
		bot.Send(msgProc)

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
		bot.Send(msgProc)

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
		bot.Send(msgProc)

		analysis, err := agent.TriageIssue(name, "Configuration Validation", data)
		if err != nil {
			resMsg = fmt.Sprintf("❌ AI pening nak check config: %v", err)
		} else {
			resMsg = fmt.Sprintf("⚙️ **ANALISIS KONFIGURASI: %s**\n\n%s", name, analysis)
		}
	case "VisualMetrics":
		metrics, _ := monitor.GetVisualMetrics()
		resMsg = metrics
	default:
		resMsg = "Action tak jumpa wtf ahaha"
	}

	msg := tgbotapi.NewMessage(callback.Message.Chat.ID, resMsg)
	bot.Send(msg)
}

// startBackgroundMonitor checks system health every minute and alerts the user.
func startBackgroundMonitor(ctx context.Context, bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64, cpuThreshold float64, cpuDuration int) {
	slog.Info("🩺 Background monitor started", "cpu_threshold", cpuThreshold, "cpu_duration_min", cpuDuration)

	previousStates, err := monitor.GetContainerStates()
	if err != nil {
		slog.Warn("Monitor init error — starting with empty state", "error", err)
		previousStates = make(map[string]monitor.ContainerStatus)
	}

	restartTracker := make(map[string]int)
	alertState := make(map[string]string)

	var mu sync.Mutex
	saveState := func() {
		mu.Lock()
		defer mu.Unlock()

		state := struct {
			Restarts map[string]int    `json:"restarts"`
			Alerts   map[string]string `json:"alerts"`
		}{
			Restarts: restartTracker,
			Alerts:   alertState,
		}
		data, _ := json.Marshal(state)
		os.WriteFile("state.json", data, 0644)
	}

	if file, err := os.ReadFile("state.json"); err == nil {
		var state struct {
			Restarts map[string]int    `json:"restarts"`
			Alerts   map[string]string `json:"alerts"`
		}
		if err := json.Unmarshal(file, &state); err == nil {
			restartTracker = state.Restarts
			alertState = state.Alerts
			slog.Info("💾 State loaded from state.json")
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
		case <-ticker.C:
		}

		currentStates, err := monitor.GetContainerStates()
		if err != nil {
			slog.Warn("Monitor loop error", "error", err)
			continue
		}

		autoPilot := os.Getenv("AUTOPILOT_ENABLED") == "true"

		for id, current := range currentStates {
			prev, exists := previousStates[id]

			if exists && prev.State == "running" && current.State != "running" {
				count := restartTracker[id]

				if autoPilot && count < 3 {
					restartTracker[id]++
					slog.Info("🤖 Auto-Pilot restart", "attempt", restartTracker[id], "container", current.Name, "id", id)

					var analysis string
					if cache, ok := rcaCache[id]; ok && time.Since(cache.Timestamp) < 10*time.Minute {
						analysis = cache.Result + "\n\n*(Nota: Analisis dari cache 10 minit terakhir)*"
					} else {
						logs, _ := actions.GetContainerLogs(id)
						var diagErr error
						analysis, diagErr = agent.DiagnoseIssue(current.Name, logs)
						if diagErr != nil {
							slog.Warn("DiagnoseIssue failed during autopilot", "container", current.Name, "error", diagErr)
							analysis = "*(Analisis AI tidak tersedia)*"
						}
						rcaCache[id] = struct {
							Result    string
							Timestamp time.Time
						}{Result: analysis, Timestamp: time.Now()}
					}

					actions.RestartContainer(id)

					msgText := fmt.Sprintf("🤖 **AUTOPILOT ACTION! (Attempt %d/3)**\n\nContainer **%s** tadi DOWN. Aku dah tolong restartkan untuk kau!\n\n**Analisis AI:**\n%s", restartTracker[id], current.Name, analysis)
					msg := tgbotapi.NewMessage(chatID, msgText)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
					alertState[id] = "autopilot_sent"
					saveState()
				} else if alertState[id] != "alert_sent" {
					alertMsg := ""
					if count >= 3 {
						alertMsg = fmt.Sprintf("🚨 **CRITICAL ALERT!**\n\nContainer **%s** dah restart 3 kali tapi still DOWN mat. 💀 Aku dah 'surrender'. Kau kena check manual jap bro.", current.Name)
					} else {
						alertMsg = fmt.Sprintf("⚠️ **ALERT!** Container **%s** dah **DOWN**! (Status: %s)\nNak aku restart ke bro?", current.Name, current.State)
					}
					alertState[id] = "alert_sent"

					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+id)
					btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Siasat Punca (RCA)", "AnalyzeRCA:"+id)
					msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnRestart), tgbotapi.NewInlineKeyboardRow(btnRCA))
					bot.Send(msg)
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
					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
				}
			}
		}

		// Sustained CPU load alert
		if monitor.GlobalMetricStore.CheckSustainedLoad(cpuThreshold, cpuDuration) {
			alertMsg := fmt.Sprintf("🔥 **SUSTAINED HIGH LOAD ALERT!**\n\nCPU load kau dah lebih **%.0f%%** untuk %d minit berturut-turut bro! Server tengah berpeluh tu. 🥵\n\nNak aku check container mana yang makan resource paling banyak?", cpuThreshold, cpuDuration)
			msg := tgbotapi.NewMessage(chatID, alertMsg)
			msg.ParseMode = "Markdown"
			btnCheck := tgbotapi.NewInlineKeyboardButtonData("📊 Check Resource Hogs", "VisualMetrics:all")
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnCheck))
			bot.Send(msg)
		}

		previousStates = currentStates
	}
}
