// Package chaos provides controlled fault-injection for Gopher-Ops. It is
// intentionally gated behind an explicit CHAOS_ENABLED env var so it can
// never fire accidentally in production.
//
// Chaos mode is NOT autopilot — it is a deliberate test tool that:
//   1. Picks a running container at random (or a named one).
//   2. Stops it to simulate a crash.
//   3. Reports back so the bot operator can watch the self-healing loop.
//
// Use case: verify that alerting + autopilot restart works end-to-end
// without manufacturing a real failure.
package chaos

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"gopher-ops/pkg/docker"

	"github.com/docker/docker/api/types/container"
)

// Enabled reports whether chaos mode is activated via env var.
func Enabled() bool {
	return os.Getenv("CHAOS_ENABLED") == "true"
}

// KillResult holds the outcome of a chaos kill.
type KillResult struct {
	ContainerID   string
	ContainerName string
	StoppedAt     time.Time
}

// KillRandom stops one random running container and returns which one was
// targeted.  Returns an error if chaos mode is disabled, no containers are
// running, or the stop fails.
//
// The caller is responsible for rate-limiting — this function does not impose
// a cooldown. Suggested: no more than once per 5 minutes in staging.
func KillRandom(ctx context.Context) (*KillResult, error) {
	if !Enabled() {
		return nil, fmt.Errorf("chaos mode disabled — set CHAOS_ENABLED=true to enable")
	}

	cli, err := docker.Get()
	if err != nil {
		return nil, err
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, fmt.Errorf("no running containers to target")
	}

	target := containers[rand.Intn(len(containers))]
	name := strings.TrimPrefix(target.Names[0], "/")

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := cli.ContainerStop(stopCtx, target.ID, container.StopOptions{}); err != nil {
		return nil, fmt.Errorf("stop %s: %w", name, err)
	}

	return &KillResult{
		ContainerID:   target.ID[:8],
		ContainerName: name,
		StoppedAt:     time.Now(),
	}, nil
}

// KillNamed stops a specific container by name or short ID.
// Returns an error if chaos mode is disabled or the container is not running.
func KillNamed(ctx context.Context, nameOrID string) (*KillResult, error) {
	if !Enabled() {
		return nil, fmt.Errorf("chaos mode disabled — set CHAOS_ENABLED=true to enable")
	}

	cli, err := docker.Get()
	if err != nil {
		return nil, err
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	for _, c := range containers {
		n := strings.TrimPrefix(c.Names[0], "/")
		if n == nameOrID || strings.HasPrefix(c.ID, nameOrID) {
			stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := cli.ContainerStop(stopCtx, c.ID, container.StopOptions{}); err != nil {
				return nil, fmt.Errorf("stop %s: %w", n, err)
			}
			return &KillResult{
				ContainerID:   c.ID[:8],
				ContainerName: n,
				StoppedAt:     time.Now(),
			}, nil
		}
	}
	return nil, fmt.Errorf("container %q not found or not running", nameOrID)
}

// FormatResult returns a Telegram-ready summary of a chaos kill.
func FormatResult(r *KillResult) string {
	return fmt.Sprintf(
		"💥 **CHAOS INJECTION**\n\n"+
			"Container **%s** (`%s`) was stopped at `%s`.\n\n"+
			"Watch the alert/autopilot loop — bot should detect and react within ~60s.",
		r.ContainerName, r.ContainerID,
		r.StoppedAt.Format("15:04:05"),
	)
}
