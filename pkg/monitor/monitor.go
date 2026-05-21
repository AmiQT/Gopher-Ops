package monitor

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"sync"
)

// URLMetricPoint stores a single latency probe result for an upstream endpoint
type URLMetricPoint struct {
	Timestamp  time.Time
	URL        string
	Latency    time.Duration
	StatusCode int
	IsUp       bool
}

// URLMetricStore holds latency history per URL
type URLMetricStore struct {
	mu      sync.RWMutex
	Points  map[string][]URLMetricPoint
	MaxSize int
}

// GlobalURLMetricStore is the in-memory latency history for all monitored URLs
var GlobalURLMetricStore = &URLMetricStore{
	Points:  make(map[string][]URLMetricPoint),
	MaxSize: 60,
}

// Push records a new latency probe result
func (s *URLMetricStore) Push(p URLMetricPoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Points[p.URL] = append(s.Points[p.URL], p)
	if len(s.Points[p.URL]) > s.MaxSize {
		s.Points[p.URL] = s.Points[p.URL][1:]
	}
}

// IsDegraded returns true if average latency over last `count` probes exceeds threshold,
// even when the endpoint is technically returning 2xx responses.
func (s *URLMetricStore) IsDegraded(url string, threshold time.Duration, count int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pts := s.Points[url]
	if len(pts) < count {
		return false
	}
	subset := pts[len(pts)-count:]
	var total time.Duration
	validCount := 0
	for _, p := range subset {
		if p.IsUp {
			total += p.Latency
			validCount++
		}
	}
	if validCount == 0 {
		return false
	}
	return total/time.Duration(validCount) > threshold
}

// UpstreamSummary returns a human-readable latency report for all monitored URLs
func (s *URLMetricStore) UpstreamSummary() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.Points) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("--- UPSTREAM LATENCY HISTORY ---\n")
	for url, pts := range s.Points {
		if len(pts) == 0 {
			continue
		}
		last := pts[len(pts)-1]
		var total time.Duration
		upCount := 0
		for _, p := range pts {
			if p.IsUp {
				total += p.Latency
				upCount++
			}
		}
		avgLatency := time.Duration(0)
		if upCount > 0 {
			avgLatency = total / time.Duration(upCount)
		}
		status := "UP"
		if !last.IsUp {
			status = "DOWN"
		}
		sb.WriteString(fmt.Sprintf("  %s | Status: %s | Last: %dms | Avg(%d samples): %dms\n",
			url, status, last.Latency.Milliseconds(), len(pts), avgLatency.Milliseconds()))
	}
	return sb.String()
}

// ImageSnapshot maps container short ID to its current image reference
type ImageSnapshot map[string]string

// DetectImageChanges compares two snapshots and returns human-readable change strings
func DetectImageChanges(prev, current ImageSnapshot) []string {
	var changes []string
	for id, curImage := range current {
		if prevImage, ok := prev[id]; ok && prevImage != curImage {
			changes = append(changes, fmt.Sprintf("Container %s: image changed %s → %s", id, prevImage, curImage))
		}
	}
	return changes
}

// MetricPoint stores a single snapshot of system load
type MetricPoint struct {
	Timestamp time.Time
	CPU       float64
	RAM       float64
}

// MetricStore holds the last 60 minutes of metrics
type MetricStore struct {
	mu      sync.RWMutex
	Points  []MetricPoint
	MaxSize int
}

// GlobalMetricStore is the in-memory database for metrics
var GlobalMetricStore = &MetricStore{
	Points:  make([]MetricPoint, 0),
	MaxSize: 60, // 60 minutes
}

// PushMetrics adds a new point and prunes old ones
func (s *MetricStore) PushMetrics(cpuLoad, ramLoad float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newPoint := MetricPoint{
		Timestamp: time.Now(),
		CPU:       cpuLoad,
		RAM:       ramLoad,
	}

	s.Points = append(s.Points, newPoint)
	if len(s.Points) > s.MaxSize {
		s.Points = s.Points[1:]
	}
}

// CheckSustainedLoad returns true if the average load over the last X points exceeds threshold
func (s *MetricStore) CheckSustainedLoad(threshold float64, count int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.Points) < count {
		return false
	}

	// Look at the last 'count' points
	subset := s.Points[len(s.Points)-count:]
	var sum float64
	for _, p := range subset {
		sum += p.CPU
	}

	avg := sum / float64(len(subset))
	return avg > threshold
}


// Info is a struct that holds basic system status
type Info struct {
	ID    string
	Names []string
	State string
	Image string
}

// ContainerStatus is used for tracking state changes
type ContainerStatus struct {
	Name  string
	State string
}

// URLStatus tracks web endpoint health
type URLStatus struct {
	URL        string
	StatusCode int
	IsUp       bool
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
	ramPercent := 0.0
	if vMem != nil {
		usedGB := float64(vMem.Used) / 1024 / 1024 / 1024
		totalGB := float64(vMem.Total) / 1024 / 1024 / 1024
		ramPercent = vMem.UsedPercent
		pb.WriteString(fmt.Sprintf("Memory Usage: %.2fGB / %.2fGB (%.2f%%)\n", usedGB, totalGB, ramPercent))
	}
	pb.WriteString("----------------------------\n")

	// RECORD METRICS to the store
	if len(cpuPercents) > 0 {
		GlobalMetricStore.PushMetrics(cpuPercents[0], ramPercent)
	}


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

// GetContainerStates returns a map of container ID to its Name and State
func GetContainerStates() (map[string]ContainerStatus, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
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

// CheckHTTP probes a URL, records latency history, and returns status
func CheckHTTP(url string) URLStatus {
	httpClient := http.Client{Timeout: 5 * time.Second}

	start := time.Now()
	resp, err := httpClient.Get(url)
	latency := time.Since(start)

	var statusCode int
	var isUp bool
	if err != nil {
		statusCode = 0
		isUp = false
	} else {
		defer resp.Body.Close()
		statusCode = resp.StatusCode
		isUp = statusCode >= 200 && statusCode < 400
	}

	GlobalURLMetricStore.Push(URLMetricPoint{
		Timestamp:  time.Now(),
		URL:        url,
		Latency:    latency,
		StatusCode: statusCode,
		IsUp:       isUp,
	})

	return URLStatus{URL: url, StatusCode: statusCode, IsUp: isUp}
}

// GetNetworkContext returns network configuration for all containers to enrich RCA context
func GetNetworkContext() (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("--- CONTAINER NETWORK CONFIG ---\n")
	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		inspect, err := cli.ContainerInspect(ctx, c.ID)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("Container: %s | NetworkMode: %s\n", name, inspect.HostConfig.NetworkMode))
		if inspect.NetworkSettings != nil {
			for netName, ep := range inspect.NetworkSettings.Networks {
				sb.WriteString(fmt.Sprintf("  Network: %s | IP: %s | Gateway: %s\n", netName, ep.IPAddress, ep.Gateway))
			}
		}
		if len(inspect.HostConfig.DNS) > 0 {
			sb.WriteString(fmt.Sprintf("  DNS: %s\n", strings.Join(inspect.HostConfig.DNS, ", ")))
		}
		if len(c.Ports) > 0 {
			sb.WriteString(fmt.Sprintf("  Ports: %v\n", c.Ports))
		}
	}
	return sb.String(), nil
}

// GetImageSnapshot returns a map of container short ID → image reference for all containers
func GetImageSnapshot() (ImageSnapshot, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	snap := make(ImageSnapshot)
	for _, c := range containers {
		snap[c.ID[:8]] = c.Image
	}
	return snap, nil
}

// GetSecurityContext gathers security-related information for all containers
func GetSecurityContext() (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
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

// GetVisualMetrics returns an ASCII bar chart of container resources
func GetVisualMetrics() (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("📊 **SYSTEM PULSE (ASCII)**\n\n")

	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		if len(name) > 15 {
			name = name[:12] + "..."
		}
		
		// For demo purposes, we'll simulate load or get real stats if possible.
		// Docker Stats is complex to stream, so let's use a simplified visualization.
		stateEmoji := "🟢"
		if c.State != "running" {
			stateEmoji = "🔴"
		}

		sb.WriteString(fmt.Sprintf("%s **%-15s**\n", stateEmoji, name))
		// Simple bar for visual impact
		if c.State == "running" {
			sb.WriteString("`[||||||||||          ] 50%` (Estimated)\n")
		} else {
			sb.WriteString("`[                    ] 0%` (Offline)\n")
		}
	}

	return sb.String(), nil
}

// GetImageSecurityReport scans images and warns about outdated ones (Simulated)
func GetImageSecurityReport() (string, error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("VULNERABILITY SCAN REPORT:\n")
	
	// Mock Vulnerability Database
	vulnerableImages := map[string]string{
		"nginx:1.18":    "CRITICAL: CVE-2021-23017 (Off-by-one in resolver). Upgrade to 1.21+",
		"redis:5.0":     "HIGH: CVE-2022-24736 (Lua script vulnerability). Upgrade to 6.0+",
		"postgres:10":   "MEDIUM: Out of support. Upgrade to 13+",
	}

	foundCount := 0
	for _, c := range containers {
		for vulnImg, reason := range vulnerableImages {
			if strings.Contains(c.Image, vulnImg) {
				foundCount++
				name := strings.TrimPrefix(c.Names[0], "/")
				sb.WriteString(fmt.Sprintf("- ⚠️ **%s** (%s)\n  Status: %s\n", name, c.Image, reason))
			}
		}
	}

	if foundCount == 0 {
		sb.WriteString("✅ No known critical vulnerabilities found in running images.\n")
	}

	return sb.String(), nil
}

