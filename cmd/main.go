package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/monitor"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	dockerclient "github.com/docker/docker/client"
	dockertypes "github.com/docker/docker/api/types"
	dockerfilters "github.com/docker/docker/api/types/filters"
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

	// 3. Start Background Monitor (metrics, HTTP probing, image snapshots)
	go startBackgroundMonitor(bot, agent, authorizedChatID)

	// 4. Start real-time Docker event listener for instant crash detection
	go startDockerEventListener(bot, agent, authorizedChatID)

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
				msgText = "📖 **COMMANDS:**\n/start - Mula sembang\n/help - Tengok ni\n/status - Check bot & AI status\n/reset - Clear state\n/silence 30m - Pause autopilot alerts (guna 0m untuk cancel)"
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
				if parseErr != nil || duration <= 0 {
					msgText = "⚠️ Format salah. Guna: `/silence 30m` atau `/silence 1h`"
				} else {
					GlobalSilence.Set(duration)
					audit.Log("manual", "Silence", "autopilot", fmt.Sprintf("silenced for %s", duration))
					msgText = fmt.Sprintf("🔕 **Silence mode aktif selama %s.**\nAutopilot alerts di-pause sehingga `%s`.\nGuna /silence 0m untuk cancel.",
						duration, GlobalSilence.Until().Format("15:04:05"))
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

// startBackgroundMonitor checks system health every X minutes and alerts the user
func startBackgroundMonitor(bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) {
	log.Println("🩺 Background monitor started...")

	// Initial state
	previousStates, err := monitor.GetContainerStates()
	if err != nil {
		log.Printf("⚠️ Monitor error during init: %v", err)
	}

	// Snapshot image versions to detect dependency shifts between cycles
	previousImages, err := monitor.GetImageSnapshot()
	if err != nil {
		log.Printf("⚠️ Monitor: failed to take initial image snapshot: %v", err)
		previousImages = make(monitor.ImageSnapshot)
	}

	// Tracker for auto-restarts and alert cooldowns
	restartTracker := make(map[string]int)
	alertState := make(map[string]string)

	// Helper to save state with Mutex to prevent race conditions
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
		// Atomic-like write: write then rename if we were on Linux,
		// but on Windows we'll just be careful with the file handle.
		os.WriteFile("state.json", data, 0644)
	}

	// Load existing state if any
	if file, err := os.ReadFile("state.json"); err == nil {
		var state struct {
			Restarts map[string]int    `json:"restarts"`
			Alerts   map[string]string `json:"alerts"`
		}
		if err := json.Unmarshal(file, &state); err == nil {
			restartTracker = state.Restarts
			alertState = state.Alerts
			log.Println("💾 State loaded from state.json")
		}
	}

	// RCA cache — keyed by containerID:exitCode to avoid stale analysis across different failure modes
	rcaCache := make(map[string]struct {
		Result    string
		Timestamp time.Time
	})

	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		currentStates, err := monitor.GetContainerStates()
		if err != nil {
			log.Printf("⚠️ Monitor loop error: %v", err)
			continue
		}

		// Detect dependency/image shifts since last cycle
		currentImages, _ := monitor.GetImageSnapshot()
		imageChanges := monitor.DetectImageChanges(previousImages, currentImages)
		if len(imageChanges) > 0 {
			log.Printf("📦 Dependency shift detected: %v", imageChanges)
		}

		// Cross-container correlation — collect all containers that just crashed this cycle
		var freshCrashes []string
		for id, current := range currentStates {
			prev, exists := previousStates[id]
			if exists && prev.State == "running" && current.State != "running" {
				freshCrashes = append(freshCrashes, current.Name)
			}
		}
		correlationCtx := ""
		if len(freshCrashes) > 1 {
			correlationCtx = fmt.Sprintf("--- CORRELATED CRASH EVENT ---\n%d containers crashed simultaneously this cycle: %s\nThis may indicate a shared upstream failure, network partition, or bad deploy.\n",
				len(freshCrashes), strings.Join(freshCrashes, ", "))
			log.Printf("🔗 Correlated crash detected: %v", freshCrashes)
		}

		autoPilot := os.Getenv("AUTOPILOT_ENABLED") == "true"

		for id, current := range currentStates {
			prev, exists := previousStates[id]

			// Detect failure
			if exists && prev.State == "running" && current.State != "running" {
				count := restartTracker[id]

				// Fetch crash signals to build a unique cache key and enrich context
				crashCtx, _ := monitor.GetCrashContext(id)
				cacheKey := fmt.Sprintf("%s:%d", id, crashCtx.ExitCode)

				if autoPilot && count < 3 {
					// --- AUTO-PILOT MODE ---
					restartTracker[id]++
					log.Printf("🤖 Auto-Pilot: Attempt #%d for %s (%s).", restartTracker[id], current.Name, id)

					// Cache keyed by containerID:exitCode — different failure modes get fresh analysis
					var analysis string
					if cache, ok := rcaCache[cacheKey]; ok && time.Since(cache.Timestamp) < 10*time.Minute {
						analysis = cache.Result + "\n\n*(Nota: Analisis dari cache 10 minit terakhir)*"
					} else {
						logs, _ := actions.GetContainerLogs(id)

						// Build all cross-signal context for Gemini
						crashSignals := monitor.FormatCrashContext(crashCtx)
						precrashMetrics := monitor.PreCrashMetricsSummary(5)
						upstreamCtx := monitor.GlobalURLMetricStore.UpstreamSummary()
						networkCtx, _ := monitor.GetNetworkContext()
						depCtx := ""
						if len(imageChanges) > 0 {
							depCtx = "--- DEPENDENCY SHIFTS DETECTED ---\n" + strings.Join(imageChanges, "\n") + "\n"
						}

						analysis, _ = agent.DiagnoseIssue(current.Name, logs,
							crashSignals, precrashMetrics, upstreamCtx, depCtx, networkCtx, correlationCtx)
						rcaCache[cacheKey] = struct {
							Result    string
							Timestamp time.Time
						}{Result: analysis, Timestamp: time.Now()}
					}

					actions.RestartContainer(id)

					// Post-restart health check after 15 seconds
					go func(containerID, containerName string, attempt int, rcaAnalysis string) {
						time.Sleep(15 * time.Second)
						if monitor.IsContainerRunning(containerID) {
							notif := fmt.Sprintf("✅ **AUTOPILOT: Restart Berjaya!**\n\nContainer **%s** dah running semula selepas attempt #%d.", containerName, attempt)
							msg := tgbotapi.NewMessage(chatID, notif)
							msg.ParseMode = "Markdown"
							bot.Send(msg)
						} else {
							notif := fmt.Sprintf("💀 **AUTOPILOT: Restart Gagal!**\n\nContainer **%s** still DOWN selepas 15 saat (attempt #%d).\n\n**RCA:**\n%s\n\nIntervention manual diperlukan.", containerName, attempt, rcaAnalysis)
							msg := tgbotapi.NewMessage(chatID, notif)
							msg.ParseMode = "Markdown"
							bot.Send(msg)
						}
					}(id, current.Name, restartTracker[id], analysis)

					msgText := fmt.Sprintf("🤖 **AUTOPILOT ACTION! (Attempt %d/3)**\n\nContainer **%s** tadi DOWN. Aku dah tolong restartkan untuk kau!\n\n**Analisis AI:**\n%s", restartTracker[id], current.Name, analysis)
					msg := tgbotapi.NewMessage(chatID, msgText)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
					alertState[id] = "autopilot_sent"
					saveState()
				} else if alertState[id] != "alert_sent" {
					// --- MANUAL/SURRENDER MODE (With Spam Protection) ---
					alertMsg := ""
					if count >= 3 {
						alertMsg = fmt.Sprintf("🚨 **CRITICAL ALERT!**\n\nContainer **%s** dah restart 3 kali tapi still DOWN mat. 💀 Aku dah 'surrender'. Kau kena check manual jap bro.", current.Name)
						alertState[id] = "alert_sent" // Mark as sent so we don't spam
					} else {
						alertMsg = fmt.Sprintf("⚠️ **ALERT!** Container **%s** dah **DOWN**! (Status: %s)\nNak aku restart ke bro?", current.Name, current.State)
						alertState[id] = "alert_sent"
					}

					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+id)
					btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Siasat Punca (RCA)", "AnalyzeRCA:"+id)
					msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnRestart), tgbotapi.NewInlineKeyboardRow(btnRCA))
					bot.Send(msg)
					saveState()
				}
			}

			// Reset counter and alert state if container is healthy
			if current.State == "running" {
				if restartTracker[id] > 0 || alertState[id] != "" {
					log.Printf("✨ %s is healthy again. Resetting trackers.", current.Name)
					restartTracker[id] = 0
					alertState[id] = ""
					saveState()
				}
			}
		}

		// --- NEW: HTTP PROBING ---
		urlsStr := os.Getenv("MONITOR_URLS")
		if urlsStr != "" {
			urls := strings.Split(urlsStr, ",")
			for _, u := range urls {
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

		// --- NEW: SUSTAINED LOAD ALERT (Idea 3) ---
		// Check if CPU average is > 80% for the last 5 checks (minutes)
		if monitor.GlobalMetricStore.CheckSustainedLoad(80.0, 5) {
			alertMsg := "🔥 **SUSTAINED HIGH LOAD ALERT!**\n\nCPU load kau dah lebih **80%** untuk 5 minit berturut-turut bro! Server tengah berpeluh tu. 🥵\n\nNak aku check container mana yang makan resource paling banyak?"
			msg := tgbotapi.NewMessage(chatID, alertMsg)
			msg.ParseMode = "Markdown"

			btnCheck := tgbotapi.NewInlineKeyboardButtonData("📊 Check Resource Hogs", "VisualMetrics:all")
			msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnCheck))

			bot.Send(msg)
		}

		previousStates = currentStates
		previousImages = currentImages
	}
}

// startDockerEventListener replaces per-minute polling for crash detection with
// Docker's real-time Events API — crashes are detected within seconds, not minutes.
func startDockerEventListener(bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) {
	log.Println("⚡ Docker event listener started...")

	for {
		if err := runDockerEventLoop(bot, agent, chatID); err != nil {
			log.Printf("⚠️ Docker event loop error: %v — reconnecting in 5s", err)
			time.Sleep(5 * time.Second)
		}
	}
}

func runDockerEventLoop(bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) error {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer cli.Close()

	filterArgs := dockerfilters.NewArgs()
	filterArgs.Add("type", "container")
	filterArgs.Add("event", "die")
	filterArgs.Add("event", "oom")

	msgCh, errCh := cli.Events(context.Background(), dockertypes.EventsOptions{Filters: filterArgs})

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

				analysis, _ = agent.DiagnoseIssue(containerName, logs,
					crashSignals, precrashMetrics, upstreamCtx, networkCtx, diskCtx, cascadeCtx)
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
