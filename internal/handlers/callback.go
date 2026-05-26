package handlers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopher-ops/internal/bot"
	"gopher-ops/internal/snooze"
	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/chaos"
	"gopher-ops/pkg/k8s"
	"gopher-ops/pkg/metrics"
	"gopher-ops/pkg/monitor"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Callback dispatches an inline-button callback. It sends its own ack +
// reply messages via d.SafeSend.
func Callback(d *bot.Deps, cb *tgbotapi.CallbackQuery) {
	d.Bot.Request(tgbotapi.NewCallback(cb.ID, "🧠 Gopher-Ops is processing..."))

	logAudit := func(act, tgt string, err error) {
		status, detail := "ok", ""
		if err != nil {
			status, detail = "error", err.Error()
		}
		audit.Log(audit.Event{Type: "action", Actor: "user", Action: act, Target: tgt, Status: status, Detail: detail})
	}

	// Snooze callback has a 3-part payload ("Snooze:<key>:<minutes>") because
	// keys themselves can contain colons (e.g. "log:abc123:OOMKill"). Handle
	// it explicitly so the generic 2-part split below doesn't reject it.
	if strings.HasPrefix(cb.Data, "Snooze:") {
		resMsg := handleSnooze(strings.TrimPrefix(cb.Data, "Snooze:"), logAudit)
		d.SafeSend(tgbotapi.NewMessage(cb.Message.Chat.ID, resMsg))
		return
	}

	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		d.SafeSend(tgbotapi.NewMessage(cb.Message.Chat.ID, "❌ Callback data rosak bro."))
		return
	}
	action, target := parts[0], parts[1]

	resMsg := dispatchCallback(d, action, target, logAudit)
	d.SafeSend(tgbotapi.NewMessage(cb.Message.Chat.ID, resMsg))
}

// handleSnooze parses a snooze payload of the form "<key>:<minutes>" and
// activates the suppression. The key may contain colons (everything before
// the trailing :<minutes>).
func handleSnooze(payload string, logAudit func(string, string, error)) string {
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return "❌ Snooze payload rosak."
	}
	key := payload[:idx]
	minutesStr := payload[idx+1:]
	minutes, err := strconv.Atoi(minutesStr)
	if err != nil || minutes <= 0 {
		return fmt.Sprintf("❌ Snooze duration tak valid: %q", minutesStr)
	}
	d := time.Duration(minutes) * time.Minute
	snooze.Set(key, d)
	logAudit("Snooze", key, nil)
	return fmt.Sprintf("🤫 Snoozed `%s` selama **%s**. Aku takkan ganggu kau pasal benda ni sampai then.", bot.SafeMD(key), humanDuration(d))
}

// humanDuration formats a duration as "1h", "4h", "24h" for short snooze
// windows. Falls back to time.Duration.String() for unusual values.
func humanDuration(d time.Duration) string {
	switch d {
	case time.Hour:
		return "1 jam"
	case 4 * time.Hour:
		return "4 jam"
	case 24 * time.Hour:
		return "24 jam"
	}
	return d.String()
}

func dispatchCallback(d *bot.Deps, action, target string, logAudit func(string, string, error)) string {
	switch action {
	case "StopContainer":
		_, err := actions.StopContainer(target)
		logAudit(action, target, err)
		if err != nil {
			return fmt.Sprintf("❌ GG! Takleh stop %s: %v", target, err)
		}
		return fmt.Sprintf("✅ Mantap! Container %s dah ditutup bro 💀", target)

	case "StartContainer":
		_, err := actions.StartContainer(target)
		logAudit(action, target, err)
		if err != nil {
			return fmt.Sprintf("❌ GG! Takleh start %s: %v", target, err)
		}
		return fmt.Sprintf("✅ LFG! Container %s dah hidup balik 🔥", target)

	case "RestartContainer":
		_, err := actions.RestartContainer(target)
		logAudit(action, target, err)
		if err != nil {
			return fmt.Sprintf("❌ GG! Takleh restart %s: %v", target, err)
		}
		return fmt.Sprintf("✅ Selesai! Container %s freshly restarted ✨", target)

	case "ClearCache":
		res := actions.ClearCache()
		logAudit(action, "", nil)
		return fmt.Sprintf("✅ Selesai! %s 🧹", res)

	case "ViewLogs":
		logs, err := actions.GetContainerLogs(target)
		if err != nil {
			return fmt.Sprintf("❌ GG! Takleh tarik log %s: %v", target, err)
		}
		return fmt.Sprintf("📜 **Logs for %s:**\n```\n%s\n```", bot.SafeMD(target), logs)

	case "AnalyzeRCA":
		name := monitor.GetContainerName(target)
		evidence := BuildRCAEvidence(d.Cfg, target)
		analysis, err := d.Agent.DiagnoseIssue(name, evidence)
		logAudit("AnalyzeRCA", target, err)
		if err != nil {
			return fmt.Sprintf("❌ Aduh, AI pening nak analyze: %v", err)
		}
		metrics.RCATotal.Inc()
		return fmt.Sprintf("🧐 **ANALISIS RCA: %s**\n\n%s", bot.SafeMD(name), analysis)

	case "TerraformApply":
		out, err := actions.TerraformApply()
		if err != nil {
			return fmt.Sprintf("❌ **Terraform Failed!**\n```\n%s\n```", out)
		}
		return fmt.Sprintf("✅ **Terraform Success!**\n```\n%s\n```", out)

	case "AuditSecurity":
		data, _ := monitor.GetSecurityContext()
		imageScanData, _ := monitor.GetImageSecurityReport()
		analysis, err := d.Agent.AuditSecurity(data, imageScanData)
		if err != nil {
			return fmt.Sprintf("❌ Aduh, AI gagal buat audit: %v", err)
		}
		return fmt.Sprintf("🛡️ **SECURITY AUDIT REPORT**\n\n%s", analysis)

	case "InvestigateNetwork":
		data := actions.InvestigateNetwork(target)
		name := monitor.GetContainerName(target)
		analysis, err := d.Agent.TriageIssue(name, "Network Connectivity", data)
		if err != nil {
			return fmt.Sprintf("❌ AI pening nak check network: %v", err)
		}
		return fmt.Sprintf("🌐 **ANALISIS RANGKAIAN: %s**\n\n%s", name, analysis)

	case "CheckConfig":
		data := actions.CheckConfig(target)
		name := monitor.GetContainerName(target)
		analysis, err := d.Agent.TriageIssue(name, "Configuration Validation", data)
		if err != nil {
			return fmt.Sprintf("❌ AI pening nak check config: %v", err)
		}
		return fmt.Sprintf("⚙️ **ANALISIS KONFIGURASI: %s**\n\n%s", name, analysis)

	case "VisualMetrics":
		out, _ := monitor.GetVisualMetrics()
		return out

	case "ContainerStats":
		return monitor.FormatContainerStats()

	case "DiskUsage":
		return monitor.FormatDiskUsage()

	case "UpdateResources":
		return fmt.Sprintf("⚙️ **Update Resources: `%s`**\n\nHantar arahan dalam format:\n`update <container> cpu <cores> mem <mb>`\n\nContoh: `update nginx cpu 0.5 mem 512`", bot.SafeMD(target))

	case "SearchLogs":
		return fmt.Sprintf("🔎 **Search Logs: `%s`**\n\nHantar pattern yang nak cari:\n`search <container> <pattern>`\n\nContoh: `search nginx error`", bot.SafeMD(target))

	case "K8sListPods":
		out, err := k8s.ListPods("")
		if err != nil {
			return fmt.Sprintf("❌ k8s error: %v", err)
		}
		return "☸️ **PODS**\n\n" + out

	case "K8sDescribePod":
		out, err := k8s.DescribePod("", target)
		if err != nil {
			return fmt.Sprintf("❌ k8s error: %v", err)
		}
		return out

	case "K8sRestartDeployment":
		out, err := k8s.RestartDeployment("", target)
		if err != nil {
			return fmt.Sprintf("❌ k8s error: %v", err)
		}
		logAudit("K8sRestartDeployment", target, nil)
		return out

	case "ChaosKillRandom":
		if !chaos.Enabled() {
			return "⚠️ Chaos mode disabled — set CHAOS_ENABLED=true to enable."
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := chaos.KillRandom(ctx)
		logAudit("ChaosKill", "random", err)
		if err != nil {
			return fmt.Sprintf("❌ Chaos kill failed: %v", err)
		}
		metrics.AlertsTotal.WithLabelValues("chaos_kill").Inc()
		return chaos.FormatResult(result)

	case "ChaosKill":
		if !chaos.Enabled() {
			return "⚠️ Chaos mode disabled — set CHAOS_ENABLED=true to enable."
		}
		result, err := chaos.KillNamed(context.Background(), target)
		logAudit("ChaosKill", target, err)
		if err != nil {
			return fmt.Sprintf("❌ Chaos kill failed: %v", err)
		}
		metrics.AlertsTotal.WithLabelValues("chaos_kill").Inc()
		return chaos.FormatResult(result)
	}
	return "Action tak jumpa wtf ahaha"
}

// buildIntentKeyboard renders an InlineKeyboardMarkup from a list of AI-emitted
// intents. Each action maps to a button with a context-rich label.
func buildIntentKeyboard(intents []ai.ActionIntent) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, intent := range intents {
		callbackData := fmt.Sprintf("%s:%s", intent.Action, intent.Target)
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
		btnText := buttonLabel(intent.Action, targetName)
		btn := tgbotapi.NewInlineKeyboardButtonData(btnText, callbackData)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// buttonLabel maps an intent action onto the user-visible button caption.
// Centralized so adding a new intent only requires one switch case.
func buttonLabel(action, targetName string) string {
	switch action {
	case "StopContainer":
		return "🛑 Stop " + targetName
	case "StartContainer":
		return "🟢 Start " + targetName
	case "RestartContainer":
		return "🔄 Restart " + targetName
	case "ClearCache":
		return "🧹 Clear OS Cache"
	case "ViewLogs":
		return "📜 View Logs " + targetName
	case "TerraformApply":
		return "🏗️ Run Terraform Apply"
	case "AuditSecurity":
		return "🛡️ Audit Security Level"
	case "VisualMetrics":
		return "📊 Show System Pulse"
	case "ContainerStats":
		return "📦 Live Container Stats"
	case "DiskUsage":
		return "💾 Show Disk Usage"
	case "UpdateResources":
		return "⚙️ Update Resources " + targetName
	case "SearchLogs":
		return "🔎 Search Logs " + targetName
	case "K8sListPods":
		return "☸️ List Pods"
	case "K8sDescribePod":
		return "☸️ Describe Pod " + targetName
	case "K8sRestartDeployment":
		return "☸️ Restart Deployment " + targetName
	case "ChaosKillRandom":
		return "💥 Chaos: Kill Random Container"
	case "ChaosKill":
		return "💥 Chaos Kill " + targetName
	case "InvestigateNetwork":
		return "🌐 Triage: Network " + targetName
	case "CheckConfig":
		return "⚙️ Triage: Config " + targetName
	}
	return action
}
