package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

// Info is a struct that holds basic system status
type Info struct {
	ID    string
	Names []string
	State string
	Image string
}

// GetSystemHealth gets an overview of containers running
func GetSystemHealth() (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", fmt.Errorf("failed to init docker client: %v", err)
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %v", err)
	}

	var pb strings.Builder
	pb.WriteString("--- SYSTEM HEALTH REPORT ---\n")
	pb.WriteString("Total Containers: " + fmt.Sprint(len(containers)) + "\n")

	for _, c := range containers {
		name := strings.Join(c.Names, ",")
		pb.WriteString(fmt.Sprintf("[%s] %s | State: %s | Status: %s\n", c.ID[:8], name, c.State, c.Status))
	}
	
	// Use REAL System Metrics instead of mock data
	pb.WriteString("\n--- REAL LATEST SYSTEM METRICS ---\n")
	
	// Get real CPU (1 second interval for accurate live reading)
	cpuPercents, _ := cpu.Percent(time.Second, false)
	if len(cpuPercents) > 0 {
		pb.WriteString(fmt.Sprintf("CPU Load: %.2f%%\n", cpuPercents[0]))
	}

	// Get real RAM
	vMem, _ := mem.VirtualMemory()
	if vMem != nil {
		usedGB := float64(vMem.Used) / 1024 / 1024 / 1024
		totalGB := float64(vMem.Total) / 1024 / 1024 / 1024
		pb.WriteString(fmt.Sprintf("Memory Usage: %.2fGB / %.2fGB\n", usedGB, totalGB))
	}
	pb.WriteString("----------------------------\n")

	return pb.String(), nil
}

// GetContainerName fetches the human-readable name of a container given its ID.
// It returns just the ID if it can't find it.
func GetContainerName(id string) string {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return id
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return id
	}

	for _, c := range containers {
		// Sometimes ID is shortened to 8-12 chars in the prompt, so use HasPrefix
		if strings.HasPrefix(c.ID, id) {
			if len(c.Names) > 0 {
				// c.Names usually look like "/test-redis"
				return c.Names[0]
			}
		}
	}
	return id
}
