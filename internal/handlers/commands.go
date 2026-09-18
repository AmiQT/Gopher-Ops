package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gopher-ops/internal/bot"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/chaos"
	"gopher-ops/pkg/k8s"
	"gopher-ops/pkg/memory"
	"gopher-ops/pkg/metrics"
	"gopher-ops/pkg/monitor"
	"gopher-ops/pkg/report"
	"gopher-ops/pkg/skills"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// StateFile is the on-disk path the monitor uses to persist alert + restart
const StateFile = "state.json"

// Command dispatches a /command message. It returns the rendered reply text;
// the caller is responsible for sending. This split keeps the function pure
// and testable — no Telegram calls happen here.
func Command(d *bot.Deps, msg *tgbotapi.Message) string {
	switch msg.Command() {
	case "start":
		return "🤖 **Gopher-Ops Ready!**\nSebut je apa kau nak aku buat kat server ni mat."

	case "help":
		return helpText()

	case "status":
		return statusText(d)

	case "containers":
		return containersText()

	case "metrics":
		return metricsText()

	case "alerts":
		return alertsText()

	case "disk":
		return monitor.FormatDiskUsage()

	case "k8s":
		return k8sText()

	case "skills":
		return skillsText()

	case "memory":
		return memoryText()

	case "reports":
		return reportsText()

	case "chaos":
		return chaosText(d)

	case "reset":
		return resetText(d)

	case "audit":
		return AuditCommand(msg.CommandArguments())

	default:
		return "Command apa tu mat? Aku tak faham ah. 😂"
	}
}

func helpText() string {
	return "📖 **COMMANDS:**\n" +
		"/start - Mula sembang\n" +
		"/help - Tengok ni\n" +
		"/status - Bot & AI status\n" +
		"/skills - List procedural SRE runbooks\n" +
		"/memory - Persistent SRE environment facts\n" +
		"/reports - Archived incident post-mortem reports\n" +
		"/containers - List semua Docker containers\n" +
		"/metrics - Live host CPU/RAM stats\n" +
		"/alerts - Active alert state\n" +
		"/disk - Disk usage per mount\n" +
		"/k8s - Kubernetes cluster info\n" +
		"/chaos - Chaos test: kill random container (CHAOS\\_ENABLED=true required)\n" +
		"/audit - Search audit log (`/audit recent`, `/audit search <q>`, `/audit verify`)\n" +
		"/reset - Clear in-memory state\n\n" +
		"**Inline commands:**\n" +
		"`update <ctr> cpu <cores> mem <mb>` - Adjust resources\n" +
		"`search <ctr> <pattern>` - Grep container logs\n" +
		"`chaos kill <name>` - Kill specific container (chaos mode)"
}

func reportsText() string {
	mgr, err := report.NewManager("reports")
	if err != nil {
		return fmt.Sprintf("❌ Error loading reports: %v", err)
	}
	res, err := mgr.ListRecent(10)
	if err != nil {
		return fmt.Sprintf("❌ Error listing reports: %v", err)
	}
	return res
}

func skillsText() string {
	mgr, err := skills.NewManager("skills")
	if err != nil {
		return fmt.Sprintf("❌ Error loading skills: %v", err)
	}
	return "📚 **GOPHER-OPS SRE SKILLS:**\n\n" + mgr.GetSummaryPrompt()
}

func memoryText() string {
	store, err := memory.NewStore("memories")
	if err != nil {
		return fmt.Sprintf("❌ Error loading memory: %v", err)
	}
	return "💾 **GOPHER-OPS PERSISTENT MEMORY:**\n\n" + store.GetPrompt()
}

func statusText(d *bot.Deps) string {
	health, _ := monitor.GetSystemHealth()
	autoPilot := "DISABLED ❌"
	if d.Cfg.AutopilotEnabled {
		autoPilot = "ACTIVE ✅ (Self-Healing On)"
	}
	aiStatus := "ONLINE ✅"
	if d.Agent.IsOffline() {
		aiStatus = "OFFLINE ⚠️ (fallback mode)"
	}
	return fmt.Sprintf("🤖 **GOPHER-OPS STATUS**\n\n"+
		"🔖 **Version:** %s\n"+
		"🧠 **AI Engine:** %s (%s) — %s\n"+
		"🤖 **Autopilot:** %s\n"+
		"👥 **Operators:** %d configured\n\n"+
		"📊 **Live Metrics:**\n%s",
		d.Version, strings.ToUpper(d.Cfg.LLMProvider), d.Cfg.LLMModel, aiStatus, autoPilot, len(d.Cfg.AuthorizedChatIDs), health)
}

func containersText() string {
	states, err := monitor.GetContainerStates()
	if err != nil {
		return fmt.Sprintf("❌ Tak dapat list containers: %v", err)
	}
	if len(states) == 0 {
		return "_(tiada containers)_"
	}
	var sb strings.Builder
	sb.WriteString("🐳 **CONTAINERS**\n\n")
	for id, cs := range states {
		emoji := "🟢"
		if cs.State != "running" {
			emoji = "🔴"
		}
		sb.WriteString(fmt.Sprintf("%s `%s` — **%s** (%s)\n", emoji, id, cs.Name, cs.State))
	}
	return sb.String()
}

func metricsText() string {
	last := monitor.GlobalMetricStore.LatestPoint()
	if last.Timestamp.IsZero() {
		return "_(metrics belum collected lagi — tunggu ~60s)_"
	}
	stats := monitor.GetContainerStatSnapshot()
	var sb strings.Builder
	sb.WriteString("📊 **LIVE METRICS**\n\n")
	sb.WriteString(fmt.Sprintf("🖥️  Host CPU: **%.1f%%**\n", last.CPU))
	sb.WriteString(fmt.Sprintf("💾  Host RAM: **%.1f%%**\n\n", last.RAM))
	if len(stats) > 0 {
		sb.WriteString("**Per Container:**\n")
		for _, s := range stats {
			sb.WriteString(fmt.Sprintf("  • **%s** CPU %.1f%% | RAM %.1f%%\n", s.Name, s.CPUPct, s.MemPct))
		}
	}
	return sb.String()
}

func alertsText() string {
	data, err := os.ReadFile(StateFile)
	if err != nil {
		return "_(tiada state aktif atau state.json tidak wujud)_"
	}
	var state struct {
		Alerts      map[string]string    `json:"alerts"`
		AlertSentAt map[string]time.Time `json:"alert_sent_at"`
	}
	if err := json.Unmarshal(data, &state); err != nil || len(state.Alerts) == 0 {
		return "✅ Tiada active alerts sekarang."
	}
	var sb strings.Builder
	sb.WriteString("🚨 **ACTIVE ALERTS**\n\n")
	for id, status := range state.Alerts {
		when := state.AlertSentAt[id].Format("15:04:05")
		sb.WriteString(fmt.Sprintf("• `%s` → **%s** (since %s)\n", id, status, when))
	}
	return sb.String()
}

func k8sText() string {
	if !k8s.Available() {
		return "⚠️ kubectl not found on PATH — install kubectl to use Kubernetes features.\n\nFor now, Kubernetes ops are available via MCP (mcp-server-kubernetes)."
	}
	info, err := k8s.GetClusterInfo()
	if err != nil {
		return fmt.Sprintf("❌ k8s error: %v", err)
	}
	return "☸️ **KUBERNETES CLUSTER**\n\n" + info
}

func chaosText(d *bot.Deps) string {
	if !chaos.Enabled() {
		return "⚠️ **Chaos mode disabled.**\nSet `CHAOS_ENABLED=true` in your .env to enable fault injection.\n\n_Gunakan dengan berhati-hati — ini akan stop container secara rawak!_"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := chaos.KillRandom(ctx)
	if err != nil {
		return fmt.Sprintf("❌ Chaos kill failed: %v", err)
	}
	audit.Log(audit.Event{
		Type:   "action",
		Actor:  "user",
		Action: "ChaosKill",
		Target: result.ContainerName,
		Status: "ok",
		Detail: "random kill via /chaos command",
	})
	metrics.AlertsTotal.WithLabelValues("chaos_kill").Inc()
	return chaos.FormatResult(result)
}

func resetText(d *bot.Deps) string {
	os.Remove(StateFile)
	select {
	case d.ResetCh <- struct{}{}:
	default: // already pending — fine
	}
	audit.Log(audit.Event{Type: "action", Actor: "user", Action: "reset_state", Status: "ok"})
	return "🧹 **STATE CLEARED!** Hafiz dah lupa sejarah lama. Kita mula hidup baru."
}
