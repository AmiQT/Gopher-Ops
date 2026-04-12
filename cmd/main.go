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

	// 1. Initialize Gemini Agent
	agent, err := ai.NewAgent(apiKey)
	if err != nil {
		log.Fatalf("❌ Failed to initialize AI Agent: %v", err)
	}
	defer agent.Close()

	// 2. Initialize Telegram Bot
	bot, err := tgbotapi.NewBotAPI(tgToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = false
	log.Printf("✅ Authorized on Telegram account %s", bot.Self.UserName)

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
			handleCallbackQuery(bot, update.CallbackQuery)
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
				bot.Send(respMsg)
				continue
			}

			// Edit message with final AI response
			respMsg := tgbotapi.NewEditMessageText(update.Message.Chat.ID, sentLoader.MessageID, response)
			
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
func handleCallbackQuery(bot *tgbotapi.BotAPI, callback *tgbotapi.CallbackQuery) {
	// Acknowledge the callback immediately so the button stops loading
	callbackConfig := tgbotapi.NewCallback(callback.ID, "")
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
	default:
		resMsg = "Action tak jumpa wtf ahaha"
	}

	msg := tgbotapi.NewMessage(callback.Message.Chat.ID, resMsg)
	bot.Send(msg)
}
