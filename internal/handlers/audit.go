package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopher-ops/internal/bot"
	"gopher-ops/pkg/audit"
)

const auditDefaultLimit = 20

// AuditCommand handles `/audit <subcommand> [args]`. Subcommands:
//   - recent [n]            — last n entries (default 20)
//   - search <query> [n]    — substring match on action/target/type/actor/detail
//   - who <actor> [n]       — entries by actor (system|user|ai)
//   - verify                — chain integrity check
//
// Returns the rendered Markdown reply. The caller sends it.
func AuditCommand(args string) string {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return auditHelp()
	}

	sub := strings.ToLower(parts[0])
	rest := parts[1:]

	switch sub {
	case "recent":
		n := parseLimit(rest, auditDefaultLimit)
		events, err := audit.Recent(n)
		return renderEvents(fmt.Sprintf("📜 **AUDIT — last %d**", n), events, err)

	case "search":
		if len(rest) == 0 {
			return "❌ Usage: `/audit search <query> [limit]`"
		}
		query, limit := parseSearchArgs(rest)
		events, err := audit.Search(query, limit)
		return renderEvents(fmt.Sprintf("🔎 **AUDIT — search `%s`**", bot.SafeMD(query)), events, err)

	case "who":
		if len(rest) == 0 {
			return "❌ Usage: `/audit who <system|user|ai> [limit]`"
		}
		actor := rest[0]
		limit := parseLimit(rest[1:], auditDefaultLimit)
		events, err := audit.ByActor(actor, limit)
		return renderEvents(fmt.Sprintf("👤 **AUDIT — actor `%s`**", bot.SafeMD(actor)), events, err)

	case "verify":
		return renderVerify(audit.Verify())

	default:
		return auditHelp()
	}
}

func auditHelp() string {
	return "📋 **AUDIT COMMANDS**\n\n" +
		"`/audit recent [n]` — last n entries (default 20)\n" +
		"`/audit search <query> [n]` — substring match across action/target/detail\n" +
		"`/audit who <actor> [n]` — filter by actor (system, user, ai)\n" +
		"`/audit verify` — chain integrity check\n\n" +
		"_Examples:_\n" +
		"`/audit recent 5`\n" +
		"`/audit search RestartContainer`\n" +
		"`/audit who ai`"
}

// parseLimit pulls an integer limit from the tail of args, falling back to
// the default. Non-numeric tail values yield the default rather than an error
// — keeps the UX forgiving.
func parseLimit(args []string, def int) int {
	if len(args) == 0 {
		return def
	}
	if n, err := strconv.Atoi(args[len(args)-1]); err == nil && n > 0 && n <= 100 {
		return n
	}
	return def
}

// parseSearchArgs splits trailing numeric limit from a multi-word query.
// "redis 5" → ("redis", 5); "container_down" → ("container_down", 20).
func parseSearchArgs(args []string) (string, int) {
	if len(args) >= 2 {
		if n, err := strconv.Atoi(args[len(args)-1]); err == nil && n > 0 && n <= 100 {
			return strings.Join(args[:len(args)-1], " "), n
		}
	}
	return strings.Join(args, " "), auditDefaultLimit
}

func renderEvents(header string, events []audit.Event, err error) string {
	if errors.Is(err, audit.ErrLogMissing) {
		return "_(audit log belum wujud — bot baru boot atau tiada activity)_"
	}
	if err != nil {
		return fmt.Sprintf("❌ Audit read failed: %v", err)
	}
	if len(events) == 0 {
		return header + "\n\n_(no matching entries)_"
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n\n")
	for _, e := range events {
		sb.WriteString(formatEvent(e))
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatEvent renders one audit row as a compact Markdown line. Untrusted
// fields (Target, Detail) are escaped via SafeMD because they can contain
// container names like `redis_node-1` that would break Markdown otherwise.
func formatEvent(e audit.Event) string {
	emoji := eventEmoji(e)
	when := e.Time.Local().Format("01-02 15:04:05")

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s `%s` **%s** by _%s_", emoji, when, e.Type, e.Actor))
	if e.Action != "" {
		sb.WriteString(fmt.Sprintf(" → `%s`", e.Action))
	}
	if e.Target != "" {
		sb.WriteString(fmt.Sprintf(" `%s`", bot.SafeMD(e.Target)))
	}
	if e.Status != "" && e.Status != "ok" {
		sb.WriteString(fmt.Sprintf(" [%s]", e.Status))
	}
	if e.Detail != "" {
		detail := e.Detail
		if len(detail) > 80 {
			detail = detail[:77] + "..."
		}
		sb.WriteString(fmt.Sprintf("\n   _%s_", bot.SafeMD(detail)))
	}
	return sb.String()
}

func eventEmoji(e audit.Event) string {
	switch e.Type {
	case "boot":
		return "🟢"
	case "shutdown":
		return "🛑"
	case "alert":
		return "🚨"
	case "autopilot":
		return "🤖"
	case "ai_tool":
		return "🧠"
	case "action":
		if e.Status == "error" {
			return "❌"
		}
		return "⚡"
	}
	return "•"
}

func renderVerify(r audit.VerifyResult) string {
	if r.ErrorMsg != "" && r.Broken < 0 {
		return fmt.Sprintf("❌ Audit verify failed: %s", r.ErrorMsg)
	}
	if r.Broken >= 0 {
		return fmt.Sprintf("🚨 **TAMPER DETECTED!**\n\nChain broken at entry **#%d** (after %d valid entries).\nReason: `%s`\n\nSomeone touched `audit.log`. Investigate immediately.",
			r.Broken, r.Entries, r.ErrorMsg)
	}
	return fmt.Sprintf("✅ **AUDIT CHAIN INTACT**\n\nVerified **%d** entries — every hash matches its predecessor. No tampering detected.", r.Entries)
}
