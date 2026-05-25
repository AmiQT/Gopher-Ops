package actions

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"gopher-ops/pkg/docker"

	"github.com/docker/docker/api/types/container"
)

const dockerTimeout = 30 * time.Second

// RestartContainer allows the AI to trigger a container restart.
func RestartContainer(containerID string) (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	if err := cli.ContainerRestart(ctx, containerID, container.StopOptions{}); err != nil {
		return "", fmt.Errorf("failed to restart %s: %w", containerID, err)
	}

	return fmt.Sprintf("Successfully triggered restart on container %s", containerID), nil
}

// StartContainer allows the AI to start a stopped container.
func StartContainer(containerID string) (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start %s: %w", containerID, err)
	}

	return fmt.Sprintf("Successfully started container %s", containerID), nil
}

// StopContainer allows the AI to stop a running container.
func StopContainer(containerID string) (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return "", fmt.Errorf("failed to stop %s: %w", containerID, err)
	}

	return fmt.Sprintf("Successfully stopped container %s", containerID), nil
}

// UpdateContainerResources adjusts the CPU and/or memory limit of a running
// container without restarting it (docker update). Either value can be 0 to
// leave it unchanged.
//
//   - cpuCores: new CPU quota in cores (e.g. 0.5 = half a core). Converted
//     internally to NanoCPUs (1 core = 1e9 NanoCPUs).
//   - memoryMB: new memory hard-limit in MB. Converted to bytes.
//
// This is intentionally gated behind AI_DESTRUCTIVE_ALLOWED or a HITL button
// in the caller — callers must check authorisation before invoking.
func UpdateContainerResources(containerID string, cpuCores float64, memoryMB int64) (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	update := container.UpdateConfig{}
	var parts []string

	if cpuCores > 0 {
		update.NanoCPUs = int64(cpuCores * 1e9)
		parts = append(parts, fmt.Sprintf("CPU → %.2f cores", cpuCores))
	}
	if memoryMB > 0 {
		update.Memory = memoryMB * 1024 * 1024
		parts = append(parts, fmt.Sprintf("RAM → %d MB", memoryMB))
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("nothing to update: provide cpuCores > 0 or memoryMB > 0")
	}

	if _, err := cli.ContainerUpdate(ctx, containerID, update); err != nil {
		return "", fmt.Errorf("failed to update resources for %s: %w", containerID, err)
	}
	return fmt.Sprintf("✅ Updated %s: %s", containerID, strings.Join(parts, ", ")), nil
}

// ClearCache is intentionally NOT implemented — cache clearing is highly
// environment-specific (Redis FLUSHALL? OS page cache? application-level?)
// and silently pretending to succeed would be dishonest. The diagnostic
// string below tells the operator exactly that.
func ClearCache() string {
	return "ℹ️ ClearCache is not implemented in this build — cache clearing depends on the target (Redis FLUSHALL, OS drop_caches, etc.). Wire up actions.ClearCache() for your stack."
}

// GetContainerLogs fetches the last 10 lines of logs for a container.
// Suitable for the "📜 View Logs" Telegram button where brevity matters.
func GetContainerLogs(containerID string) (string, error) {
	return GetContainerLogsN(containerID, 10)
}

// GetContainerLogsN fetches the last `lines` lines of logs. Used by the RCA
// pipeline which needs much deeper context than the view-logs button.
func GetContainerLogsN(containerID string, lines int) (string, error) {
	if lines <= 0 {
		lines = 10
	}

	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprint(lines),
	}

	out, err := cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", fmt.Errorf("failed to get logs for %s: %w", containerID, err)
	}
	defer out.Close()

	buf := new(bytes.Buffer)
	if _, err = io.Copy(buf, out); err != nil {
		return "", fmt.Errorf("failed to read logs for %s: %w", containerID, err)
	}

	return buf.String(), nil
}

// SearchLogs fetches the last `tailLines` log lines for a container and returns
// only lines that match the given pattern (case-insensitive substring match).
// Returns at most maxResults matching lines to keep Telegram messages bounded.
// The AI can call this directly during investigation to surface specific errors.
func SearchLogs(containerID, pattern string, tailLines, maxResults int) (string, error) {
	if tailLines <= 0 {
		tailLines = 500
	}
	if maxResults <= 0 {
		maxResults = 20
	}

	raw, err := GetContainerLogsN(containerID, tailLines)
	if err != nil {
		return "", err
	}

	patLower := strings.ToLower(pattern)
	var matched []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(strings.ToLower(line), patLower) {
			matched = append(matched, strings.TrimSpace(line))
			if len(matched) >= maxResults {
				break
			}
		}
	}

	if len(matched) == 0 {
		return fmt.Sprintf("_(no matches for %q in last %d log lines)_", pattern, tailLines), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔎 **%d match(es)** for `%s` in `%s`:\n\n", len(matched), pattern, containerID))
	sb.WriteString("```\n")
	for _, l := range matched {
		if len(l) > 200 {
			l = l[:197] + "..."
		}
		sb.WriteString(l + "\n")
	}
	sb.WriteString("```")
	return sb.String(), nil
}

// InvestigateNetwork returns the container's real network configuration via
// docker inspect: connected networks, IP/gateway per network, port bindings,
// DNS overrides, and a connectivity assessment based on the running state.
func InvestigateNetwork(containerID string) string {
	cli, err := docker.Get()
	if err != nil {
		return fmt.Sprintf("NETWORK ANALYSIS: docker client unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Sprintf("NETWORK ANALYSIS: inspect failed: %v", err)
	}

	var sb strings.Builder
	name := strings.TrimPrefix(inspect.Name, "/")
	sb.WriteString(fmt.Sprintf("NETWORK ANALYSIS for %s:\n", name))
	sb.WriteString(fmt.Sprintf("- Running: %v\n", inspect.State.Running))

	if inspect.NetworkSettings == nil || len(inspect.NetworkSettings.Networks) == 0 {
		sb.WriteString("- Networks: none attached\n")
	} else {
		sb.WriteString(fmt.Sprintf("- Networks (%d):\n", len(inspect.NetworkSettings.Networks)))
		for netName, cfg := range inspect.NetworkSettings.Networks {
			sb.WriteString(fmt.Sprintf("  * %s: IP=%s, Gateway=%s, MAC=%s\n",
				netName, cfg.IPAddress, cfg.Gateway, cfg.MacAddress))
			if len(cfg.Aliases) > 0 {
				sb.WriteString(fmt.Sprintf("    Aliases: %s\n", strings.Join(cfg.Aliases, ", ")))
			}
		}
	}

	if inspect.NetworkSettings != nil && len(inspect.NetworkSettings.Ports) > 0 {
		sb.WriteString("- Port Bindings:\n")
		for port, bindings := range inspect.NetworkSettings.Ports {
			if len(bindings) == 0 {
				sb.WriteString(fmt.Sprintf("  * %s (exposed, not published)\n", port))
				continue
			}
			for _, b := range bindings {
				host := b.HostIP
				if host == "" {
					host = "0.0.0.0"
				}
				sb.WriteString(fmt.Sprintf("  * %s → %s:%s\n", port, host, b.HostPort))
			}
		}
	}

	if len(inspect.HostConfig.DNS) > 0 {
		sb.WriteString(fmt.Sprintf("- DNS Overrides: %s\n", strings.Join(inspect.HostConfig.DNS, ", ")))
	}

	return sb.String()
}

// CheckConfig returns the container's real launch configuration via docker
// inspect: image, entrypoint, command, working dir, user, environment
// variables (with secret redaction), mounts, labels, and restart policy.
func CheckConfig(containerID string) string {
	cli, err := docker.Get()
	if err != nil {
		return fmt.Sprintf("CONFIG ANALYSIS: docker client unavailable: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	inspect, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Sprintf("CONFIG ANALYSIS: inspect failed: %v", err)
	}

	var sb strings.Builder
	name := strings.TrimPrefix(inspect.Name, "/")
	sb.WriteString(fmt.Sprintf("CONFIG ANALYSIS for %s:\n", name))
	sb.WriteString(fmt.Sprintf("- Image: %s\n", inspect.Config.Image))

	if len(inspect.Config.Entrypoint) > 0 {
		sb.WriteString(fmt.Sprintf("- Entrypoint: %v\n", inspect.Config.Entrypoint))
	}
	if len(inspect.Config.Cmd) > 0 {
		sb.WriteString(fmt.Sprintf("- Cmd: %v\n", inspect.Config.Cmd))
	}
	if inspect.Config.WorkingDir != "" {
		sb.WriteString(fmt.Sprintf("- Working Dir: %s\n", inspect.Config.WorkingDir))
	}
	user := inspect.Config.User
	if user == "" {
		user = "root (default)"
	}
	sb.WriteString(fmt.Sprintf("- User: %s\n", user))

	if len(inspect.Config.Env) > 0 {
		sb.WriteString("- Environment:\n")
		for _, e := range inspect.Config.Env {
			sb.WriteString("  * " + redactSecret(e) + "\n")
		}
	}

	if len(inspect.Mounts) > 0 {
		sb.WriteString("- Mounts:\n")
		for _, m := range inspect.Mounts {
			sb.WriteString(fmt.Sprintf("  * %s → %s (%s, %s)\n",
				m.Source, m.Destination, m.Type, m.Mode))
		}
	}

	if len(inspect.Config.Labels) > 0 {
		sb.WriteString("- Labels:\n")
		for k, v := range inspect.Config.Labels {
			sb.WriteString(fmt.Sprintf("  * %s=%s\n", k, v))
		}
	}

	rp := inspect.HostConfig.RestartPolicy
	sb.WriteString(fmt.Sprintf("- Restart Policy: %s (max retries: %d)\n",
		rp.Name, rp.MaximumRetryCount))

	return sb.String()
}

// redactSecret masks values for env vars whose names suggest credentials. The
// log-and-prompt pipeline forwards these to the AI and Telegram, so leaking a
// raw password into the chat would be a real incident.
func redactSecret(envVar string) string {
	parts := strings.SplitN(envVar, "=", 2)
	if len(parts) != 2 {
		return envVar
	}
	upper := strings.ToUpper(parts[0])
	sensitive := []string{"PASSWORD", "SECRET", "TOKEN", "API_KEY", "PRIVATE", "CREDENTIAL"}
	for _, needle := range sensitive {
		if strings.Contains(upper, needle) {
			return parts[0] + "=***REDACTED***"
		}
	}
	return envVar
}
