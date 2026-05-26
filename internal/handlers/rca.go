// Package handlers contains the Telegram command, inline-message, and
// callback-query handlers. Each handler takes a *bot.Deps and returns no
// shared state, so they're trivial to test in isolation.
package handlers

import (
	"fmt"
	"strings"

	"gopher-ops/internal/config"
	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/monitor"
)

// BuildRCAEvidence assembles the inspect post-mortem, system metric snapshot,
// and a deep log tail into a single text bundle for the AI. This is what
// upgrades RCA from "guess from 10 log lines" to "reason about exit codes,
// OOM flags, restart history, system load, and 200 lines of context."
func BuildRCAEvidence(cfg *config.Config, containerID string) string {
	var sb strings.Builder

	if crash, err := monitor.GetCrashContext(containerID); err == nil {
		sb.WriteString(crash.FormatForAI())
		sb.WriteString("\n")
	} else {
		sb.WriteString("=== CONTAINER POST-MORTEM ===\n(inspect unavailable: " + err.Error() + ")\n\n")
	}

	sb.WriteString(monitor.GetMetricsSnapshot(15))
	sb.WriteString("\n")

	logs, err := actions.GetContainerLogsN(containerID, cfg.RCALogLines)
	if err != nil {
		sb.WriteString("=== CONTAINER LOGS ===\n(unavailable: " + err.Error() + ")\n")
	} else {
		sb.WriteString(fmt.Sprintf("=== CONTAINER LOGS (last %d lines) ===\n%s\n", cfg.RCALogLines, logs))
	}

	return sb.String()
}
