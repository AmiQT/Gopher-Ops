package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/monitor"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"time"
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

	// 3. Start Background Monitor
	go startBackgroundMonitor(bot, agent, authorizedChatID)

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
		
		msgProc := tgbotapi.NewMessage(callback.Message.Chat.ID, "🛡️ *Gopher-Ops sedang melakukan audit keselamatan...*")
		msgProc.ParseMode = "Markdown"
		bot.Send(msgProc)

		analysis, err := agent.AuditSecurity(data)
		if err != nil {
			resMsg = fmt.Sprintf("❌ Aduh, AI gagal buat audit: %v", err)
		} else {
			resMsg = fmt.Sprintf("🛡️ **SECURITY AUDIT REPORT**\n\n%s", analysis)
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

// startBackgroundMonitor checks system health every X minutes and alerts the user
func startBackgroundMonitor(bot *tgbotapi.BotAPI, agent ai.AIAgent, chatID int64) {
	log.Println("🩺 Background monitor started...")
	
	// Initial state
	previousStates, err := monitor.GetContainerStates()
	if err != nil {
		log.Printf("⚠️ Monitor error during init: %v", err)
	}

	ticker := time.NewTicker(1 * time.Minute)
	for range ticker.C {
		currentStates, err := monitor.GetContainerStates()
		if err != nil {
			log.Printf("⚠️ Monitor loop error: %v", err)
			continue
		}

		autoPilot := os.Getenv("AUTOPILOT_ENABLED") == "true"

		for id, current := range currentStates {
			prev, exists := previousStates[id]
			
			// Detect if container state changed to something bad (exited/dead)
			if exists && prev.State == "running" && current.State != "running" {
				if autoPilot {
					// --- AUTO-PILOT MODE ---
					log.Printf("🤖 Auto-Pilot: Detecting failure in %s (%s). Attempting auto-fix...", current.Name, id)
					
					// 1. Analyze RCA
					logs, _ := actions.GetContainerLogs(id)
					analysis, _ := agent.DiagnoseIssue(current.Name, logs)
					
					// 2. Perform Restart
					actions.RestartContainer(id)
					
					// 3. Notify User
					msgText := fmt.Sprintf("🤖 **AUTOPILOT ACTION!**\n\nContainer **%s** tadi DOWN. Aku dah tolong restartkan untuk kau!\n\n**Analisis AI:**\n%s", current.Name, analysis)
					msg := tgbotapi.NewMessage(chatID, msgText)
					msg.ParseMode = "Markdown"
					bot.Send(msg)
				} else {
					// --- MANUAL MODE ---
					alertMsg := fmt.Sprintf("⚠️ **ALERT!** Container **%s** (%s) dah **DOWN**! (Status: %s)\nNak aku restart ke bro?", current.Name, id, current.State)
					
					msg := tgbotapi.NewMessage(chatID, alertMsg)
					msg.ParseMode = "Markdown"
					
					// Add Buttons
					btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+id)
					btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Siasat Punca (RCA)", "AnalyzeRCA:"+id)
					
					msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
						tgbotapi.NewInlineKeyboardRow(btnRestart),
						tgbotapi.NewInlineKeyboardRow(btnRCA),
					)
					
					bot.Send(msg)
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

		previousStates = currentStates
	}
}
