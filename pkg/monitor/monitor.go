package monitor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"gopher-ops/pkg/docker"

	"github.com/docker/docker/api/types/container"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
)

const dockerTimeout = 30 * time.Second

// MetricPoint stores a single snapshot of system load.
type MetricPoint struct {
	Timestamp time.Time
	CPU       float64
	RAM       float64
}

// MetricStore holds a rolling window of system metrics.
type MetricStore struct {
	mu      sync.RWMutex
	Points  []MetricPoint
	MaxSize int
}

// GlobalMetricStore is the in-memory database for metrics.
var GlobalMetricStore = &MetricStore{
	Points:  make([]MetricPoint, 0),
	MaxSize: 60,
}

// SetMetricsHistorySize updates the rolling window size.
func SetMetricsHistorySize(n int) {
	GlobalMetricStore.mu.Lock()
	defer GlobalMetricStore.mu.Unlock()
	GlobalMetricStore.MaxSize = n
}

// PushMetrics adds a new data point and prunes old ones.
func (s *MetricStore) PushMetrics(cpuLoad, ramLoad float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Points = append(s.Points, MetricPoint{
		Timestamp: time.Now(),
		CPU:       cpuLoad,
		RAM:       ramLoad,
	})
	if len(s.Points) > s.MaxSize {
		s.Points = s.Points[1:]
	}
}

// LatestPoint returns the most recent MetricPoint, or a zero value if the store is empty.
func (s *MetricStore) LatestPoint() MetricPoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.Points) == 0 {
		return MetricPoint{}
	}
	return s.Points[len(s.Points)-1]
}

// CheckSustainedLoad returns true if the average CPU over the last N points exceeds threshold.
func (s *MetricStore) CheckSustainedLoad(threshold float64, count int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.Points) < count {
		return false
	}

	subset := s.Points[len(s.Points)-count:]
	var sum float64
	for _, p := range subset {
		sum += p.CPU
	}
	return (sum / float64(len(subset))) > threshold
}

// Info holds basic container status.
type Info struct {
	ID    string
	Names []string
	State string
	Image string
}

// ContainerStatus is used for tracking state changes.
type ContainerStatus struct {
	Name  string
	State string
}

// URLStatus tracks web endpoint health.
type URLStatus struct {
	URL        string
	StatusCode int
	IsUp       bool
}

// GetSystemHealth returns an overview of containers and system metrics.
func GetSystemHealth() (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	var pb strings.Builder
	pb.WriteString("--- SYSTEM HEALTH REPORT ---\n")
	pb.WriteString("Total Containers: " + fmt.Sprint(len(containers)) + "\n")

	for _, c := range containers {
		name := strings.Join(c.Names, ",")
		pb.WriteString(fmt.Sprintf("[%s] %s | State: %s | Status: %s\n", c.ID[:8], name, c.State, c.Status))
	}

	pb.WriteString("\n--- REAL LATEST SYSTEM METRICS ---\n")

	cpuPercents, _ := cpu.Percent(time.Second, false)
	if len(cpuPercents) > 0 {
		pb.WriteString(fmt.Sprintf("CPU Load: %.2f%%\n", cpuPercents[0]))
	}

	vMem, _ := mem.VirtualMemory()
	ramPercent := 0.0
	if vMem != nil {
		usedGB := float64(vMem.Used) / 1024 / 1024 / 1024
		totalGB := float64(vMem.Total) / 1024 / 1024 / 1024
		ramPercent = vMem.UsedPercent
		pb.WriteString(fmt.Sprintf("Memory Usage: %.2fGB / %.2fGB (%.2f%%)\n", usedGB, totalGB, ramPercent))
	}
	pb.WriteString("----------------------------\n")

	if len(cpuPercents) > 0 {
		GlobalMetricStore.PushMetrics(cpuPercents[0], ramPercent)
	}

	return pb.String(), nil
}

// GetContainerName fetches the human-readable name for a container ID.
// Returns the ID itself if the name cannot be resolved.
func GetContainerName(id string) string {
	cli, err := docker.Get()
	if err != nil {
		return id
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return id
	}

	for _, c := range containers {
		if strings.HasPrefix(c.ID, id) {
			if len(c.Names) > 0 {
				return c.Names[0]
			}
		}
	}
	return id
}

// GetContainerStates returns a map of container ID to its Name and State.
func GetContainerStates() (map[string]ContainerStatus, error) {
	cli, err := docker.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	states := make(map[string]ContainerStatus)
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		states[c.ID[:8]] = ContainerStatus{
			Name:  name,
			State: c.State,
		}
	}
	return states, nil
}

// CheckHTTP probes a URL and returns its status.
func CheckHTTP(url string) URLStatus {
	client := http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return URLStatus{URL: url, StatusCode: 0, IsUp: false}
	}
	defer resp.Body.Close()

	isUp := resp.StatusCode >= 200 && resp.StatusCode < 400
	return URLStatus{URL: url, StatusCode: resp.StatusCode, IsUp: isUp}
}

// GetSecurityContext gathers security-related information for all containers.
func GetSecurityContext() (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("SECURITY AUDIT DATA:\n")
	for _, c := range containers {
		inspect, _ := cli.ContainerInspect(ctx, c.ID)
		name := strings.TrimPrefix(c.Names[0], "/")

		isPrivileged := inspect.HostConfig.Privileged
		user := inspect.Config.User
		if user == "" {
			user = "root (default)"
		}

		sb.WriteString(fmt.Sprintf("- Container: %s\n  Image: %s\n  User: %s\n  Privileged: %v\n  Ports: %v\n",
			name, c.Image, user, isPrivileged, c.Ports))
	}

	return sb.String(), nil
}

// asciiBar builds a 20-char ASCII progress bar for the given percentage.
func asciiBar(pct float64) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 5) // 20 chars → each char = 5 %
	bar := make([]byte, 20)
	for i := range bar {
		if i < filled {
			bar[i] = '|'
		} else {
			bar[i] = ' '
		}
	}
	return string(bar)
}

// GetVisualMetrics returns a per-container CPU/RAM bar chart using live Docker
// stats, with host-level CPU/RAM from GlobalMetricStore shown below.
func GetVisualMetrics() (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	// Index cached per-container stats by short ID for O(1) lookup.
	statsByID := make(map[string]ContainerStat)
	for _, s := range GetContainerStatSnapshot() {
		statsByID[s.ID] = s
	}

	// Grab host-level figures.
	GlobalMetricStore.mu.RLock()
	var latestCPU, latestRAM float64
	if n := len(GlobalMetricStore.Points); n > 0 {
		latestCPU = GlobalMetricStore.Points[n-1].CPU
		latestRAM = GlobalMetricStore.Points[n-1].RAM
	}
	GlobalMetricStore.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("📊 **SYSTEM PULSE (ASCII)**\n\n")

	running, stopped := 0, 0
	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		shortID := c.ID[:8]

		if c.State == "running" {
			running++
			sb.WriteString(fmt.Sprintf("🟢 **%s**\n", name))
			if stat, ok := statsByID[shortID]; ok {
				sb.WriteString(fmt.Sprintf("   CPU: `[%-20s]` %.1f%%\n", asciiBar(stat.CPUPct), stat.CPUPct))
				if stat.MemLimitMB > 0 {
					sb.WriteString(fmt.Sprintf("   RAM: `[%-20s]` %.1f%% (%.0f/%.0f MB)\n",
						asciiBar(stat.MemPct), stat.MemPct, stat.MemUsedMB, stat.MemLimitMB))
				} else {
					sb.WriteString(fmt.Sprintf("   RAM: %.0f MB (no limit)\n", stat.MemUsedMB))
				}
			} else {
				sb.WriteString("   _(stats pending next collection cycle)_\n")
			}
		} else {
			stopped++
			sb.WriteString(fmt.Sprintf("🔴 **%s** (%s)\n", name, c.Status))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("🖥️  **Host:** %d running, %d stopped\n", running, stopped))
	sb.WriteString(fmt.Sprintf("   CPU: `[%-20s]` %.1f%%\n", asciiBar(latestCPU), latestCPU))
	sb.WriteString(fmt.Sprintf("   RAM: `[%-20s]` %.1f%%\n", asciiBar(latestRAM), latestRAM))

	if latestCPU == 0 && latestRAM == 0 {
		sb.WriteString("   _(host metrics not yet collected — check again in ~60 s)_\n")
	}

	return sb.String(), nil
}

// GetImageSecurityReport is now in trivy.go — it uses Trivy when available,
// falling back to a hardcoded pattern list otherwise.
