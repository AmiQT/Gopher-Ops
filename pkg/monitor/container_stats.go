package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"gopher-ops/pkg/docker"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// ContainerStat holds a single real-time snapshot for one container.
type ContainerStat struct {
	ID        string
	Name      string
	State     string
	CPUPct    float64
	MemUsedMB float64
	MemLimitMB float64
	MemPct    float64
	CollectedAt time.Time
}

// containerStatStore is a protected map of the latest stat per container ID.
type containerStatStore struct {
	mu    sync.RWMutex
	stats map[string]ContainerStat
}

var globalContainerStats = &containerStatStore{
	stats: make(map[string]ContainerStat),
}

// calcCPUPercent derives CPU% from a Docker stats snapshot using the standard
// delta formula: (cpuDelta / systemDelta) * numCPUs * 100.
func calcCPUPercent(s *dockertypes.StatsJSON) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) -
		float64(s.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(s.CPUStats.SystemUsage) -
		float64(s.PreCPUStats.SystemUsage)

	if systemDelta <= 0 || cpuDelta < 0 {
		return 0
	}

	numCPUs := float64(s.CPUStats.OnlineCPUs)
	if numCPUs == 0 {
		numCPUs = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if numCPUs == 0 {
		numCPUs = 1
	}

	return (cpuDelta / systemDelta) * numCPUs * 100.0
}

// fetchOneStat pulls a single stats snapshot for one container.
// ContainerStatsOneShot avoids waiting for two polling intervals (unlike stream=false).
func fetchOneStat(ctx context.Context, id, name, state string) (ContainerStat, error) {
	cli, err := docker.Get()
	if err != nil {
		return ContainerStat{}, err
	}

	resp, err := cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return ContainerStat{}, err
	}
	defer resp.Body.Close()

	var raw dockertypes.StatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return ContainerStat{}, fmt.Errorf("decode stats for %s: %w", name, err)
	}

	cpuPct := calcCPUPercent(&raw)

	memUsed := float64(raw.MemoryStats.Usage) / 1024 / 1024
	memLimit := float64(raw.MemoryStats.Limit) / 1024 / 1024
	memPct := 0.0
	if raw.MemoryStats.Limit > 0 {
		memPct = float64(raw.MemoryStats.Usage) / float64(raw.MemoryStats.Limit) * 100.0
	}

	return ContainerStat{
		ID:         id[:8],
		Name:       name,
		State:      state,
		CPUPct:     cpuPct,
		MemUsedMB:  memUsed,
		MemLimitMB: memLimit,
		MemPct:     memPct,
		CollectedAt: time.Now(),
	}, nil
}

// CollectContainerStats fetches stats for all running containers in parallel
// and stores them in globalContainerStats for fast read-access.
func CollectContainerStats(ctx context.Context) error {
	cli, err := docker.Get()
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(listCtx, container.ListOptions{All: false})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	type result struct {
		stat ContainerStat
		err  error
	}

	ch := make(chan result, len(containers))
	for _, c := range containers {
		c := c
		name := strings.TrimPrefix(c.Names[0], "/")
		go func() {
			// Each goroutine gets its own short deadline so one slow container
			// can't hold up the entire collection round.
			sCtx, sCancel := context.WithTimeout(ctx, 5*time.Second)
			defer sCancel()
			stat, err := fetchOneStat(sCtx, c.ID, name, c.State)
			ch <- result{stat, err}
		}()
	}

	globalContainerStats.mu.Lock()
	defer globalContainerStats.mu.Unlock()
	for range containers {
		r := <-ch
		if r.err == nil {
			globalContainerStats.stats[r.stat.ID] = r.stat
		}
	}
	return nil
}

// GetContainerStatSnapshot returns a copy of all cached per-container stats.
func GetContainerStatSnapshot() []ContainerStat {
	globalContainerStats.mu.RLock()
	defer globalContainerStats.mu.RUnlock()

	out := make([]ContainerStat, 0, len(globalContainerStats.stats))
	for _, s := range globalContainerStats.stats {
		out = append(out, s)
	}
	return out
}

// FormatContainerStats returns a Telegram-ready text table of live container metrics.
func FormatContainerStats() string {
	stats := GetContainerStatSnapshot()
	if len(stats) == 0 {
		return "_(no running containers or stats not yet collected)_"
	}

	var sb strings.Builder
	sb.WriteString("📦 **LIVE CONTAINER METRICS**\n\n")
	for _, s := range stats {
		age := time.Since(s.CollectedAt).Round(time.Second)
		sb.WriteString(fmt.Sprintf("🟢 **%s**\n", s.Name))
		sb.WriteString(fmt.Sprintf("   CPU : `[%-20s]` %.1f%%\n", asciiBar(s.CPUPct), s.CPUPct))
		if s.MemLimitMB > 0 {
			sb.WriteString(fmt.Sprintf("   RAM : `[%-20s]` %.1f%% (%.0f / %.0f MB)\n",
				asciiBar(s.MemPct), s.MemPct, s.MemUsedMB, s.MemLimitMB))
		} else {
			sb.WriteString(fmt.Sprintf("   RAM : %.0f MB (no limit set)\n", s.MemUsedMB))
		}
		sb.WriteString(fmt.Sprintf("   _(collected %s ago)_\n\n", age))
	}
	return sb.String()
}
