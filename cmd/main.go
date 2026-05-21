package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/monitor"

	dockerclient "github.com/docker/docker/client"
	dockerfilters "github.com/docker/docker/api/types/filters"
	dockertypes "github.com/docker/docker/api/types"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

func main() {
	fmt.Println("🤖 Booting up Gopher-Ops SRE Telegram Bot...")

	err := godotenv.Load()
	if err != nil {
		log.Println("⚠️  No .env file found. Make sure credentials are set!")
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("❌ GEMINI_API_KEY is not set.")
	}

	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tgToken == "" {
		log.Fatal("❌ TELEGRAM_BOT_TOKEN is not set.")
	}

	authIDStr := os.Getenv("AUTHORIZED_CHAT_ID")
	if authIDStr == "" {
		log.Fatal("❌ AUTHORIZED_CHAT_ID is not set in .env. Setup required for production safety.")
	}
	authorizedChatID, err := strconv.ParseInt(authIDStr, 10, 64)
	if err != nil {
		log.Fatalf("❌ AUTHORIZED_CHAT_ID must be a valid number. Error: %v", err)
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

	// Adjust API key if using local
	if llmProvider == "local" {
		localKey := os.Getenv("LOCAL_LLM_API_KEY")
		if localKey != "" {
			aiCfg.APIKey = localKey
		} else {
			aiCfg.APIKey = "lm-studio" // Default for LM Studio
		}
	}

	agent, err := ai.NewAgent(aiCfg)
	if err != nil {
		log.Fatalf("❌ Failed to initialize AI Agent (%s): %v", llmProvider, err)
	}
	defer agent.Close()

	// 2. Initialize Telegram Bot
	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("✅ Authorized on Telegram account %s", bot.Self.UserName)

	// 3. Graceful shutdown context — cancelled on SIGTERM/SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
		s := <-sig
		log.Printf("🛑 Signal %s received — shutting down gracefully...", s)
		cancel()
		bot.StopReceivingUpdates()
	}()

	// 4. Start Background Monitor (metrics, HTTP probing, image snapshots only — no crash detection)
	go startBackgroundMonitor(ctx, bot, agent, authorizedChatID)

	// 5. Start real-time Docker event listener for instant crash detection
	go startDockerEventListener(ctx, bot, agent, authorizedChatID)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	// Keep track of some simple state. In a real app you'd map by ChatID
	for update := range updates {
		if update.CallbackQuery != nil {
			// Security Check for Callbacks
			if update.CallbackQuery.Message.Chat.ID != authorizedChatID {
				callbackConfig := tgbotapi.NewCallback(update.CallbackQuery.ID, "❌ Kau siapa siot?")
				bot.Request(callbackConfig)
				continue
			}

			// User clicked an inline button
			handleCallbackQuery(bot, agent, update.CallbackQuery)
			continue
		}

		if update.Message == nil { // ignore any non-Message updates
			continue
		}

		// Security Check for Messages
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
				msgText = "📖 **COMMANDS:**\n/start - Mula sembang\n/help - Tengok ni\n/status - Check bot & AI status\n/reset - Clear state\n/silence 30m - Pause autopilot (guna 0m untuk cancel)\n/audit - Tengok 10 tindakan AI terbaru\n/degraded - Tengok latency upstream sekarang"
			case "status":
				// Get live metrics for status
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
			case "silence":
				args := update.Message.CommandArguments()
				duration, parseErr := time.ParseDuration(args)
				if parseErr != nil {
					msgText = "⚠️ Format salah. Guna: `/silence 30m` atau `/silence 1h`"
				} else if duration == 0 {
					GlobalSilence.Set(0)
					audit.Log("manual", "Silence", "autopilot", "cancelled")
					msgText = "🔔 **Silence dibatalkan.** Autopilot alerts aktif balik."
				} else {
					GlobalSilence.Set(duration)
					audit.Log("manual", "Silence", "autopilot", fmt.Sprintf("silenced for %s", duration))
					msgText = fmt.Sprintf("🔕 **Silence mode aktif selama %s.**\nAutopilot alerts di-pause sehingga `%s`.\nGuna `/silence 0s` untuk cancel.",
						duration, GlobalSilence.Until().Format("15:04:05"))
				}
			case "audit":
				entries := audit.ReadLast(10)
				if len(entries) == 0 {
					msgText = "📋 Tiada rekod audit lagi."
				} else {
					var sb strings.Builder
					sb.WriteString("📋 **10 Tindakan AI Terbaru:**\n\n")
					for _, e := range entries {
						sb.WriteString(fmt.Sprintf("`%s` **%s** → %s (%s)\n", e.Timestamp[11:19], e.Action, e.Target, e.Trigger))
					}
					msgText = sb.String()
				}
			case "degraded":
				summary := monitor.GlobalURLMetricStore.UpstreamSummary()
				if summary == "" {
					msgText = "🌐 Tiada data upstream lagi. Pastikan `MONITOR_URLS` dah di-set dalam .env."
				} else {
					msgText = "🌐 **UPSTREAM LATENCY STATUS:**\n\n```\n" + summary + "```"
				}
			default:
				msgText = "Command apa tu mat? Aku tak faham ah. 😂"
			}
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, msgText)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
			continue
		}

		if !update.Message.IsCommand() {
			// Normal chat message
			input := strings.TrimSpace(update.Message.Text)

			if input == "clear" {
				agent.ResetSession()
				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "🧹 Gopher-Ops: Chat memory cleared! Token history reset jadi 0. Jimat token LFG! ✨")
				msg.ParseMode = "Markdown"
				bot.Send(msg)
				continue
			}

			// Send a loading message
			msgLoader := tgbotapi.NewMessage(update.Message.Chat.ID, "🧠 Gopher-Ops is thinking...")
			sentLoader, _ := bot.Send(msgLoader)

			// Process via AI
			response, intents, err := agent.ProcessRequest(input)
			if err != nil {
				respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, fmt.Sprintf("❌ Error borak: %v", err))
				respMsg.ParseMode = "Markdown"
				bot.Send(respMsg)
				continue
			}

			// Edit message with final AI response
			respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, response)
			respMsg.ParseMode = "Markdown"

			// Build Inline Keyboard if there are intentions
			if len(intents) > 0 {
				var rows [][]tgbotapi.InlineKeyboardButton
				for _, intent := range intents {
					callbackData := fmt.Sprintf("%s:%s", intent.Action, intent.Target) // e.g. "StopContainer:0ccfe811"

					btnText := ""
					targetName := intent.Target
					if intent.Target != "" {
						// Attempt to get a human-readable name like "/test-redis" instead of just "0ccfe811"
						foundName := monitor.GetContainerName(intent.Target)
						if foundName != "" && foundName != intent.Target {
							// safe slicing
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

// handleCallbackQuery processes button clicks from the user in Telegram
func handleCallbackQuery(bot *tgbotapi.BotAPI, agent ai.AIAgent, callback *tgbotapi.CallbackQuery) {
	// Acknowledge the callback immediately so the button stops loading
	callbackConfig := tgbotapi.NewCallback(callback.ID, "🧠 Gopher-Ops is processing...")
	bot.Request(callbackConfig)

	// Parse Action:Target
	parts := strings.Split(callback.Data, ":")
	if len(parts) != 2 {
		bot.Send(tgbotapi.NewMessage(callback.Message.Chat.ID, "❌ Callback data rosak bro."))
		return
	}

	action := parts[0]
	target := parts[1]

	var resMsg string
	// Execute Action
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

		// Send a "Processing" message because AI takes time
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
		// Show plan first — operator must confirm before apply
		planOut, err := actions.TerraformPlan()
		if err != nil {
			resMsg = fmt.Sprintf("❌ **Terraform Plan Failed!**\n```\n%s\n```", planOut)
		} else {
			preview := planOut
			if len(preview) > 2000 {
				preview = preview[:2000] + "\n... (truncated)"
			}
			planMsg := tgbotapi.NewMessage(callback.Message.Chat.ID,
				fmt.Sprintf("📋 **TERRAFORM PLAN OUTPUT:**\n```\n%s\n```\n\n⚠️ Review plan di atas. Tekan **Confirm** untuk apply.", preview))
			planMsg.ParseMode = "Markdown"
			btnConfirm := tgbotapi.NewInlineKeyboardButtonData("✅ Confirm Apply", "TerraformConfirm:now")
			btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "TerraformCancel:now")
			planMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnConfirm, btnCancel))
			bot.Send(planMsg)
			audit.Log("manual", "TerraformPlan", "terraform", "plan shown to operator")
			return
		}
	case "TerraformConfirm":
		out, err := actions.TerraformApply()
		if err != nil {
			resMsg = fmt.Sprintf("❌ **Terraform Apply Failed!**\n```\n%s\n```", out)
			audit.Log("manual", "TerraformApply", "terraform", "failed")
		} else {
			resMsg = fmt.Sprintf("✅ **Terraform Apply Success!**\n```\n%s\n```", out)
			audit.Log("manual", "TerraformApply", "terraform", "success")
		}
	case "TerraformCancel":
		resMsg = "🚫 Terraform apply dibatalkan oleh operator."
		audit.Log("manual", "TerraformCancel", "terraform", "cancelled")
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

// silenceState controls the maintenance/silence window for autopilot alerts
type silenceState struct {
	mu    sync.Mutex
	until time.Time
}

func (s *silenceState) IsSilenced() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.until)
}

func (s *silenceState) Set(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.until = time.Now().Add(d)
}

func (s *silenceState) Until() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.until
}

// GlobalSilence is the shared silence state accessible from command handlers and the monitor
var GlobalSilence = &silenceState{}

// startBackgroundMonitor handles periodic tasks only — metrics, HTTP probing, image snapshots.
// All crash detection is handled exclusively by startDockerEventListener to avoid double-restart races.
func startBackgroundMonitor(ctx context.Context, bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) {
	log.Println("🩺 Background monitor started...")

	// Load persisted state (restarts, alerts, silence window)
	type persistedState struct {
		Restarts     map[string]int    `json:"restarts"`
		Alerts       map[string]string `json:"alerts"`
		SilenceUntil time.Time         `json:"silence_until,omitempty"`
	}

	state := persistedState{
		Restarts: make(map[string]int),
		Alerts:   make(map[string]string),
	}
	if file, err := os.ReadFile("state.json"); err == nil {
		if err := json.Unmarshal(file, &state); err == nil {
			// Restore silence window if it hasn't expired
			if time.Now().Before(state.SilenceUntil) {
				remaining := time.Until(state.SilenceUntil)
				GlobalSilence.Set(remaining)
				log.Printf("💾 State loaded — silence restored for %s", remaining.Round(time.Second))
			} else {
				log.Println("💾 State loaded from state.json")
			}
		}
	}

	// atomicSave writes state.json via a temp-file + rename to avoid corruption on crash
	atomicSave := func() {
		state.SilenceUntil = GlobalSilence.Until()
		data, err := json.Marshal(state)
		if err != nil {
			return
		}
		tmp, err := os.CreateTemp(".", "state.json.tmp.*")
		if err != nil {
			return
		}
		if _, err = tmp.Write(data); err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			return
		}
		tmp.Close()
		os.Rename(tmp.Name(), "state.json")
	}

	previousImages, err := monitor.GetImageSnapshot()
	if err != nil {
		log.Printf("⚠️ Monitor: failed initial image snapshot: %v", err)
		previousImages = make(monitor.ImageSnapshot)
	}

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("🩺 Background monitor stopped.")
			return
		case <-ticker.C:
		}

		// Collect metrics (CPU/RAM/disk recorded inside GetSystemHealth)
		monitor.GetSystemHealth() //nolint — side-effect: pushes metrics to GlobalMetricStore

		// Detect dependency/image shifts
		currentImages, _ := monitor.GetImageSnapshot()
		changes := monitor.DetectImageChanges(previousImages, currentImages)
		if len(changes) > 0 {
			log.Printf("📦 Dependency shift: %v", changes)
		}
		previousImages = currentImages

		// HTTP probing with latency recording
		urlsStr := os.Getenv("MONITOR_URLS")
		if urlsStr != "" {
			for _, u := range strings.Split(urlsStr, ",") {
				u = strings.TrimSpace(u)
				status := monitor.CheckHTTP(u)
				if !status.IsUp && !GlobalSilence.IsSilenced() {
					alertMsg := fmt.Sprintf("🌐 **HTTP ALERT!** `%s` return error!\nStatus Code: **%d**", u, status.StatusCode)
					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
				}
			}
		}

		// Latency degradation alert — upstream slow but alive
		if urlsStr != "" && !GlobalSilence.IsSilenced() {
			for _, u := range strings.Split(urlsStr, ",") {
				u = strings.TrimSpace(u)
				if monitor.GlobalURLMetricStore.IsDegraded(u, 2*time.Second, 5) {
					alertMsg := fmt.Sprintf("🐢 **LATENCY ALERT!** `%s` avg response >2s untuk 5 probe berturut-turut.\nService mungkin degraded walaupun technically UP.", u)
					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
				}
			}
		}

		// Sustained CPU load alert
		if monitor.GlobalMetricStore.CheckSustainedLoad(80.0, 5) && !GlobalSilence.IsSilenced() {
			alertMsg := "🔥 **SUSTAINED HIGH LOAD!**\n\nCPU >80% untuk 5 minit berturut-turut. Server berpeluh ni."
			m := tgbotapi.NewMessage(chatID, alertMsg)
			m.ParseMode = "Markdown"
			btnCheck := tgbotapi.NewInlineKeyboardButtonData("📊 Check Resource Hogs", "VisualMetrics:all")
			m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnCheck))
			bot.Send(m)
		}

		_ = state
		_ = atomicSave
	}
}

// startDockerEventListener is the sole owner of crash detection and autopilot logic.
// It uses Docker's real-time Events API — crashes are detected within seconds, not minutes.
func startDockerEventListener(ctx context.Context, bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) {
	log.Println("⚡ Docker event listener started...")

	for {
		select {
		case <-ctx.Done():
			log.Println("⚡ Docker event listener stopped.")
			return
		default:
		}

		if err := runDockerEventLoop(ctx, bot, agent, chatID); err != nil {
			if ctx.Err() != nil {
				return // graceful shutdown
			}
			log.Printf("⚠️ Docker event loop error: %v — reconnecting in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func runDockerEventLoop(ctx context.Context, bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) error {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer cli.Close()

	filterArgs := dockerfilters.NewArgs()
	filterArgs.Add("type", "container")
	filterArgs.Add("event", "die")
	filterArgs.Add("event", "oom")

	msgCh, errCh := cli.Events(ctx, dockertypes.EventsOptions{Filters: filterArgs})

	restartTracker := make(map[string]int)
	rcaCache := make(map[string]struct {
		Result    string
		Timestamp time.Time
	})

	for {
		select {
		case msg := <-msgCh:
			if GlobalSilence.IsSilenced() {
				log.Printf("🔕 Silence active — skipping event for %s", msg.Actor.Attributes["name"])
				continue
			}

			containerID := msg.Actor.ID
			if len(containerID) > 8 {
				containerID = containerID[:8]
			}
			containerName := msg.Actor.Attributes["name"]
			autoPilot := os.Getenv("AUTOPILOT_ENABLED") == "true"
			count := restartTracker[containerID]

			// Fetch all context signals
			crashCtx, _ := monitor.GetCrashContext(containerID)
			cacheKey := fmt.Sprintf("%s:%d", containerID, crashCtx.ExitCode)

			var analysis string
			if cache, ok := rcaCache[cacheKey]; ok && time.Since(cache.Timestamp) < 10*time.Minute {
				analysis = cache.Result + "\n\n*(Nota: Analisis dari cache 10 minit terakhir)*"
			} else {
				logs, _ := actions.GetContainerLogs(containerID)
				crashSignals := monitor.FormatCrashContext(crashCtx)
				precrashMetrics := monitor.PreCrashMetricsSummary(5)
				upstreamCtx := monitor.GlobalURLMetricStore.UpstreamSummary()
				networkCtx, _ := monitor.GetNetworkContext()
				diskCtx := monitor.GetDiskUsage()

				deps, _ := monitor.GetContainerDependencies()
				cascadeCtx := monitor.FormatCascadeContext([]string{containerName}, deps)

				analysis, err = agent.DiagnoseIssue(containerName, logs,
					crashSignals, precrashMetrics, upstreamCtx, networkCtx, diskCtx, cascadeCtx)
				if err != nil {
					log.Printf("⚠️ DiagnoseIssue failed for %s: %v", containerName, err)
					analysis = fmt.Sprintf("⚠️ *RCA gagal: %v*\nSila semak logs secara manual.", err)
				}
				rcaCache[cacheKey] = struct {
					Result    string
					Timestamp time.Time
				}{Result: analysis, Timestamp: time.Now()}
			}

			if autoPilot && count < 3 {
				restartTracker[containerID]++
				actions.RestartContainer(containerID)
				audit.Log("autopilot", "RestartContainer", containerName, fmt.Sprintf("attempt %d", restartTracker[containerID]))

				msgText := fmt.Sprintf("⚡ **REAL-TIME ALERT! AUTOPILOT (Attempt %d/3)**\n\nContainer **%s** baru je crash. Aku dah restart!\n\n**Analisis AI:**\n%s",
					restartTracker[containerID], containerName, analysis)
				m := tgbotapi.NewMessage(chatID, msgText)
				m.ParseMode = "Markdown"
				bot.Send(m)

				// Post-restart health check
				go func(cID, cName string, attempt int, rca string) {
					time.Sleep(15 * time.Second)
					if monitor.IsContainerRunning(cID) {
						audit.Log("autopilot", "HealthCheck", cName, "recovered")
						m := tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ **%s** dah running semula (attempt #%d).", cName, attempt))
						m.ParseMode = "Markdown"
						bot.Send(m)
					} else {
						audit.Log("autopilot", "HealthCheck", cName, "still_down")
						triggerEscalation(bot, chatID, cName, attempt, rca)
					}
				}(containerID, containerName, restartTracker[containerID], analysis)

			} else {
				audit.Log("monitor", "Alert", containerName, "manual_intervention_required")
				alertMsg := fmt.Sprintf("⚡ **REAL-TIME CRASH DETECTED!**\n\nContainer **%s** baru crash!\n\n**Analisis AI:**\n%s\n\nNak aku restart?", containerName, analysis)
				if count >= 3 {
					alertMsg = fmt.Sprintf("🚨 **CRITICAL!** **%s** dah crash berkali-kali. Autopilot surrender. Kau kena intervene manual bro.\n\n**RCA:**\n%s", containerName, analysis)
					triggerEscalation(bot, chatID, containerName, count, analysis)
				}
				m := tgbotapi.NewMessage(chatID, alertMsg)
				m.ParseMode = "Markdown"
				btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+containerID)
				btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 RCA Detail", "AnalyzeRCA:"+containerID)
				m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(btnRestart),
					tgbotapi.NewInlineKeyboardRow(btnRCA),
				)
				bot.Send(m)
			}

		case err := <-errCh:
			return err
		}
	}
}

// triggerEscalation sends a critical alert to Telegram and optionally to a webhook
func triggerEscalation(bot *tgbotapi.BotAPI, chatID int64, containerName string, attempts int, rca string) {
	msg := tgbotapi.NewMessage(chatID,
		fmt.Sprintf("🚨 **ESCALATION ALERT!**\n\nContainer **%s** gagal recover selepas %d percubaan.\n\n**RCA:**\n%s\n\nIntervention manual diperlukan segera.", containerName, attempts, rca))
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	audit.Log("escalation", "EscalationAlert", containerName, fmt.Sprintf("failed after %d attempts", attempts))

	// Post to webhook if configured
	webhookURL := os.Getenv("ESCALATION_WEBHOOK_URL")
	if webhookURL == "" {
		return
	}

	payload := fmt.Sprintf(`{"container":"%s","attempts":%d,"rca":"%s"}`, containerName, attempts, rca)
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(payload))
	if err != nil {
		log.Printf("⚠️ Escalation webhook failed: %v", err)
		return
	}
	defer resp.Body.Close()
	log.Printf("📣 Escalation webhook fired for %s — HTTP %d", containerName, resp.StatusCode)
}
