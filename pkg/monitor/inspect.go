package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopher-ops/pkg/docker"
)

// CrashContext captures the post-mortem state of a container via `docker inspect`.
// This is the data the AI needs to distinguish OOM kills from app crashes from
// healthcheck failures.
type CrashContext struct {
	ID           string
	Name         string
	Image        string
	State        string
	ExitCode     int
	OOMKilled    bool
	Error        string
	RestartCount int
	StartedAt    string
	FinishedAt   string
	Health       string
	HealthLog    []string
	MemoryLimit  int64
	CPULimit     int64
}

// GetCrashContext returns rich post-mortem data via Docker inspect.
func GetCrashContext(id string) (*CrashContext, error) {
	cli, err := docker.Get()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	inspect, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", id, err)
	}

	c := &CrashContext{
		ID:           inspect.ID,
		Name:         strings.TrimPrefix(inspect.Name, "/"),
		Image:        inspect.Config.Image,
		State:        inspect.State.Status,
		ExitCode:     inspect.State.ExitCode,
		OOMKilled:    inspect.State.OOMKilled,
		Error:        inspect.State.Error,
		RestartCount: inspect.RestartCount,
		StartedAt:    inspect.State.StartedAt,
		FinishedAt:   inspect.State.FinishedAt,
		MemoryLimit:  inspect.HostConfig.Memory,
		CPULimit:     inspect.HostConfig.NanoCPUs,
	}

	if inspect.State.Health != nil {
		c.Health = inspect.State.Health.Status
		// Keep only the most recent 5 probes to bound prompt size
		log := inspect.State.Health.Log
		start := 0
		if len(log) > 5 {
			start = len(log) - 5
		}
		for _, h := range log[start:] {
			c.HealthLog = append(c.HealthLog, fmt.Sprintf("[%s] exit=%d: %s",
				h.End.Format(time.RFC3339), h.ExitCode, strings.TrimSpace(h.Output)))
		}
	}

	return c, nil
}

// FormatForAI renders the crash context as a structured text block that an LLM
// can reason about. Exit-code hints are inlined since most LLMs don't have
// strong priors over Unix signal numbers.
func (c *CrashContext) FormatForAI() string {
	var sb strings.Builder
	sb.WriteString("=== CONTAINER POST-MORTEM ===\n")
	sb.WriteString(fmt.Sprintf("- Name: %s\n", c.Name))
	sb.WriteString(fmt.Sprintf("- Image: %s\n", c.Image))
	sb.WriteString(fmt.Sprintf("- Current State: %s\n", c.State))
	sb.WriteString(fmt.Sprintf("- Exit Code: %d %s\n", c.ExitCode, exitCodeHint(c.ExitCode)))
	sb.WriteString(fmt.Sprintf("- OOMKilled: %v\n", c.OOMKilled))
	if c.Error != "" {
		sb.WriteString(fmt.Sprintf("- Docker Error: %s\n", c.Error))
	}
	sb.WriteString(fmt.Sprintf("- Restart Count (lifetime): %d\n", c.RestartCount))
	sb.WriteString(fmt.Sprintf("- Started At: %s\n", c.StartedAt))
	sb.WriteString(fmt.Sprintf("- Finished At: %s\n", c.FinishedAt))
	if c.MemoryLimit > 0 {
		sb.WriteString(fmt.Sprintf("- Memory Limit: %d MB\n", c.MemoryLimit/1024/1024))
	} else {
		sb.WriteString("- Memory Limit: unlimited\n")
	}
	if c.CPULimit > 0 {
		sb.WriteString(fmt.Sprintf("- CPU Limit: %.2f cores\n", float64(c.CPULimit)/1e9))
	}
	if c.Health != "" {
		sb.WriteString(fmt.Sprintf("- Health: %s\n", c.Health))
		if len(c.HealthLog) > 0 {
			sb.WriteString("- Recent Health Probes:\n")
			for _, h := range c.HealthLog {
				sb.WriteString("    " + h + "\n")
			}
		}
	}
	return sb.String()
}

func exitCodeHint(code int) string {
	switch code {
	case 0:
		return "(clean exit)"
	case 1:
		return "(generic application error)"
	case 125:
		return "(docker daemon error)"
	case 126:
		return "(container command not executable)"
	case 127:
		return "(container command not found)"
	case 137:
		return "(SIGKILL — likely OOM or forced kill)"
	case 139:
		return "(SIGSEGV — segmentation fault)"
	case 143:
		return "(SIGTERM — graceful termination)"
	default:
		return ""
	}
}

// GetMetricsSnapshot returns a text snapshot of the last N metric points,
// suitable for inlining into an AI prompt to give it temporal context.
func GetMetricsSnapshot(n int) string {
	GlobalMetricStore.mu.RLock()
	defer GlobalMetricStore.mu.RUnlock()

	if len(GlobalMetricStore.Points) == 0 {
		return "=== SYSTEM METRICS ===\n(no metrics recorded yet)\n"
	}
	if n > len(GlobalMetricStore.Points) {
		n = len(GlobalMetricStore.Points)
	}
	points := GlobalMetricStore.Points[len(GlobalMetricStore.Points)-n:]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== SYSTEM METRICS (last %d min) ===\n", len(points)))
	for _, p := range points {
		sb.WriteString(fmt.Sprintf("  %s  CPU=%5.1f%%  RAM=%5.1f%%\n",
			p.Timestamp.Format("15:04:05"), p.CPU, p.RAM))
	}
	return sb.String()
}
