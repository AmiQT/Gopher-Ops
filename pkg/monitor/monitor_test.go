package monitor

import (
	"strings"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
)

func TestGetContainerName_Invalid(t *testing.T) {
	invalidID := "abc123invalid_docker_id"
	name := GetContainerName(invalidID)
	if name != invalidID {
		t.Errorf("expected %q, got %q", invalidID, name)
	}
}

func TestAsciiBar(t *testing.T) {
	cases := []struct {
		pct      float64
		wantLen  int
		wantFill int
	}{
		{0, 20, 0},
		{50, 20, 10},
		{100, 20, 20},
		{105, 20, 20}, // clamp above 100
		{-5, 20, 0},  // clamp below 0
	}
	for _, c := range cases {
		bar := asciiBar(c.pct)
		if len(bar) != c.wantLen {
			t.Errorf("asciiBar(%.0f) len=%d, want %d", c.pct, len(bar), c.wantLen)
		}
		filled := strings.Count(bar, "|")
		if filled != c.wantFill {
			t.Errorf("asciiBar(%.0f) filled=%d, want %d", c.pct, filled, c.wantFill)
		}
	}
}

func TestCalcCPUPercent_Zero(t *testing.T) {
	// When system delta is 0 (no time elapsed), CPU% must be 0, not NaN or Inf.
	s := &dockertypes.StatsJSON{}
	got := calcCPUPercent(s)
	if got != 0 {
		t.Errorf("calcCPUPercent with zero delta = %f, want 0", got)
	}
}

func TestCalcCPUPercent_Basic(t *testing.T) {
	s := &dockertypes.StatsJSON{}
	s.CPUStats.CPUUsage.TotalUsage = 2_000_000_000
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000_000_000
	s.CPUStats.SystemUsage = 10_000_000_000
	s.PreCPUStats.SystemUsage = 0
	s.CPUStats.OnlineCPUs = 4

	// cpuDelta=1e9, systemDelta=1e10, numCPUs=4 → 1e9/1e10*4*100 = 40%
	got := calcCPUPercent(s)
	if got < 39.9 || got > 40.1 {
		t.Errorf("calcCPUPercent = %.2f, want ~40.0", got)
	}
}

func TestFormatContainerStats_Empty(t *testing.T) {
	// With an empty store the output should not panic and should say "no running containers".
	globalContainerStats.mu.Lock()
	globalContainerStats.stats = make(map[string]ContainerStat)
	globalContainerStats.mu.Unlock()

	out := FormatContainerStats()
	if !strings.Contains(out, "no running containers") {
		t.Errorf("FormatContainerStats empty case = %q, want 'no running containers'", out)
	}
}
