package actions

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// ClearCache simulates a cleanup or cache clearing operation.
func ClearCache() string {
	return "System cache cleared successfully. Memory impact should decrease."
}

// GetContainerLogs fetches the last 10 lines of logs for a container.
func GetContainerLogs(containerID string) (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "10",
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

// InvestigateNetwork simulates checking connectivity for a container.
func InvestigateNetwork(containerID string) string {
	return "NETWORK ANALYSIS: Container can reach external network. No obvious DNS or Routing issues found."
}

// CheckConfig simulates a configuration validation check.
func CheckConfig(containerID string) string {
	return "CONFIG ANALYSIS: Found /etc/nginx/nginx.conf. Syntax is OK. No conflicting upstream rules detected."
}
