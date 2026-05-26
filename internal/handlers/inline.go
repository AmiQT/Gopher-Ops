package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopher-ops/internal/bot"
	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/chaos"
	"gopher-ops/pkg/metrics"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Inline tries to handle one of the inline text commands (update, search,
// chaos kill, clear). Returns (replyText, parseMode, handled). When
// handled==false, the caller should fall through to AI processing.
func Inline(d *bot.Deps, input string) (string, string, bool) {
	lower := strings.ToLower(input)

	if input == "clear" {
		d.Agent.ResetSession()
		return "🧹 Gopher-Ops: Chat memory cleared! Token history reset jadi 0. Jimat token LFG! ✨", "Markdown", true
	}

	if strings.HasPrefix(lower, "update ") {
		if text, ok := handleUpdate(input); ok {
			return text, "", true
		}
	}

	if strings.HasPrefix(lower, "search ") {
		if text, ok := handleSearch(input); ok {
			return text, "Markdown", true
		}
	}

	if strings.HasPrefix(lower, "chaos kill ") {
		if text, ok := handleChaosKill(input); ok {
			return text, "Markdown", true
		}
	}

	return "", "", false
}

func handleUpdate(input string) (string, bool) {
	parts := strings.Fields(input)
	if len(parts) < 6 {
		return "", false
	}
	ctrID := parts[1]
	var cpuCores float64
	var memMB int64
	for i := 2; i+1 < len(parts); i++ {
		switch strings.ToLower(parts[i]) {
		case "cpu":
			cpuCores, _ = strconv.ParseFloat(parts[i+1], 64)
		case "mem", "memory":
			memMB, _ = strconv.ParseInt(parts[i+1], 10, 64)
		}
	}
	result, err := actions.UpdateContainerResources(ctrID, cpuCores, memMB)
	if err != nil {
		return fmt.Sprintf("❌ Update failed: %v", err), true
	}
	audit.Log(audit.Event{Type: "action", Actor: "user", Action: "update_resources", Target: ctrID, Status: "ok"})
	return result, true
}

func handleSearch(input string) (string, bool) {
	parts := strings.SplitN(input, " ", 3)
	if len(parts) != 3 {
		return "", false
	}
	ctrID, pattern := parts[1], parts[2]
	result, err := actions.SearchLogs(ctrID, pattern, 500, 20)
	if err != nil {
		return fmt.Sprintf("❌ Search failed: %v", err), true
	}
	return result, true
}

func handleChaosKill(input string) (string, bool) {
	parts := strings.SplitN(input, " ", 3)
	if len(parts) != 3 {
		return "", false
	}
	targetName := parts[2]
	if !chaos.Enabled() {
		return "⚠️ Chaos mode disabled — set CHAOS_ENABLED=true to use chaos injection.", true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := chaos.KillNamed(ctx, targetName)
	if err != nil {
		return fmt.Sprintf("❌ Chaos kill failed: %v", err), true
	}
	audit.Log(audit.Event{
		Type: "action", Actor: "user", Action: "ChaosKill",
		Target: result.ContainerName, Status: "ok",
		Detail: "named kill via chaos kill inline command",
	})
	metrics.AlertsTotal.WithLabelValues("chaos_kill").Inc()
	return chaos.FormatResult(result), true
}

// AIProcess handles a free-form message by dispatching it to the LLM agent
// and rendering the response (with any suggested action buttons) into an
// editable Telegram message.
func AIProcess(d *bot.Deps, msg *tgbotapi.Message) {
	loader := tgbotapi.NewMessage(msg.Chat.ID, "🧠 Gopher-Ops is thinking...")
	sent, _ := d.SafeSend(loader)

	response, intents, err := d.Agent.ProcessRequest(msg.Text)
	if err != nil {
		edit := tgbotapi.NewEditMessageText(msg.Chat.ID, sent.MessageID, fmt.Sprintf("❌ Error borak: %v", err))
		edit.ParseMode = "Markdown"
		d.SafeSend(edit)
		return
	}

	edit := tgbotapi.NewEditMessageText(msg.Chat.ID, sent.MessageID, response)
	edit.ParseMode = "Markdown"

	if len(intents) > 0 {
		keyboard := buildIntentKeyboard(intents)
		edit.ReplyMarkup = &keyboard
	}
	d.SafeSend(edit)
}
