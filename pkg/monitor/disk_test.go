package monitor

import (
	"strings"
	"testing"
)

// TestGetDiskUsage_FiltersPseudoFS asserts that the real-filesystem filter
// rejects every entry in the skipFS list so output isn't polluted with
// tmpfs/cgroup/proc noise.
func TestGetDiskUsage_FiltersPseudoFS(t *testing.T) {
	usages, err := GetDiskUsage()
	if err != nil {
		t.Skipf("skipping — disk inspection unavailable in this environment: %v", err)
	}
	for _, u := range usages {
		if skipFS[u.Filesystem] {
			t.Errorf("pseudo fs %q leaked through filter at mount %q", u.Filesystem, u.Mount)
		}
	}
}

// TestCheckDiskAlerts_ZeroThresholdAlertsEverything pins the threshold-handling
// contract: passing 0 falls back to the default (85%), it does NOT alert on
// every mount. A naive "if pct >= 0" would alert constantly and bury real signals.
func TestCheckDiskAlerts_ZeroThresholdUsesDefault(t *testing.T) {
	alerts, err := CheckDiskAlerts(0)
	if err != nil {
		t.Skipf("skipping — disk inspection unavailable: %v", err)
	}
	// Sanity: every reported alert must actually be >= the default threshold.
	for _, a := range alerts {
		if a.UsedPct < defaultDiskAlertThreshold {
			t.Errorf("alert with usedPct=%.1f below default threshold %.1f",
				a.UsedPct, defaultDiskAlertThreshold)
		}
	}
}

// TestFormatDiskUsage_NonEmpty verifies the formatter emits expected headers
// and never panics on a live read.
func TestFormatDiskUsage_NonEmpty(t *testing.T) {
	out := FormatDiskUsage()
	if !strings.Contains(out, "DISK USAGE") && !strings.Contains(out, "no real filesystems") {
		t.Errorf("unexpected disk usage output: %q", out)
	}
}
