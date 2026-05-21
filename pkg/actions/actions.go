package actions

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"io"
	"bytes"
)

// RestartContainer allows the AI to trigger a container restart.
// We are mimicking a self-healing action here.
func RestartContainer(containerID string) (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to init docker client: %v", err)
	}
	defer cli.Close()

	if err := cli.ContainerRestart(ctx, containerID, container.StopOptions{}); err != nil {
		return "", fmt.Errorf("failed to restart %s: %v", containerID, err)
	}

	return fmt.Sprintf("Successfully triggered restart on container %s", containerID), nil
}

// StartContainer allows the AI to start a stopped container.
func StartContainer(containerID string) (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to init docker client: %v", err)
	}
	defer cli.Close()

	if err := cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start %s: %v", containerID, err)
	}

	return fmt.Sprintf("Successfully started container %s", containerID), nil
}
func StopContainer(containerID string) (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to init docker client: %v", err)
	}
	defer cli.Close()

	// Stop container
	if err := cli.ContainerStop(ctx, containerID, container.StopOptions{}); err != nil {
		return "", fmt.Errorf("failed to stop %s: %v", containerID, err)
	}

	return fmt.Sprintf("Successfully stopped container %s", containerID), nil
}

// ClearCache simulates a cleanup or cache clearing operation.
func ClearCache() string {
	// Normally this could run `redis-cli flushall` or similar inside an exec command.
	return "System cache cleared successfully. Memory impact should decrease."
}

// GetContainerLogs fetches the last 100 lines of logs for a container
func GetContainerLogs(containerID string) (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       "100",
	}

	out, err := cli.ContainerLogs(ctx, containerID, options)
	if err != nil {
		return "", err
	}
	defer out.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, out)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// InvestigateNetwork simulates checking connectivity for a container
func InvestigateNetwork(containerID string) string {
	// Mock: Check if container can reach google.com or similar
	return "NETWORK ANALYSIS: Container can reach external network. No obvious DNS or Routing issues found."
}

// CheckConfig simulates a configuration validation check
func CheckConfig(containerID string) string {
	// Mock: Check common config files
	return "CONFIG ANALYSIS: Found /etc/nginx/nginx.conf. Syntax is OK. No conflicting upstream rules detected."
}

