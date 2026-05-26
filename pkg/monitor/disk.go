package monitor

import (
	"fmt"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
)

// DiskUsage holds usage stats for one mounted filesystem.
type DiskUsage struct {
	Mount      string
	Filesystem string
	UsedGB     float64
	TotalGB    float64
	UsedPct    float64
}

// skipFS lists pseudo-filesystems that operators don't care about and would
// just clutter the output. tmpfs/proc/sysfs aren't "real disks" running out.
var skipFS = map[string]bool{
	"tmpfs":       true,
	"devtmpfs":    true,
	"proc":        true,
	"sysfs":       true,
	"cgroup":      true,
	"cgroup2":     true,
	"overlay":     true, // docker overlay layers — usage rolls up to / anyway
	"squashfs":    true,
	"autofs":      true,
	"devpts":      true,
	"mqueue":      true,
	"securityfs":  true,
	"fusectl":     true,
	"pstore":      true,
	"bpf":         true,
	"tracefs":     true,
	"debugfs":     true,
	"hugetlbfs":   true,
	"ramfs":       true,
	"rpc_pipefs":  true,
	"binfmt_misc": true,
	"configfs":    true,
	"nsfs":        true,
}

// GetDiskUsage returns usage for every real (non-pseudo) mounted filesystem.
// The /proc fs filter is necessary because gopsutil reports ~40 entries on a
// typical Linux box and 90% are cgroup/tmpfs noise.
func GetDiskUsage() ([]DiskUsage, error) {
	parts, err := disk.Partitions(false) // false = physical only
	if err != nil {
		return nil, fmt.Errorf("read partitions: %w", err)
	}

	var out []DiskUsage
	seen := make(map[string]bool) // de-dup bind mounts
	for _, p := range parts {
		if skipFS[p.Fstype] {
			continue
		}
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true

		u, err := disk.Usage(p.Mountpoint)
		if err != nil || u == nil {
			continue // unreadable mount (e.g. permission denied) — skip silently
		}

		out = append(out, DiskUsage{
			Mount:      p.Mountpoint,
			Filesystem: p.Fstype,
			UsedGB:     float64(u.Used) / 1024 / 1024 / 1024,
			TotalGB:    float64(u.Total) / 1024 / 1024 / 1024,
			UsedPct:    u.UsedPercent,
		})
	}
	return out, nil
}

// FormatDiskUsage renders the disk-usage report for Telegram.
func FormatDiskUsage() string {
	usages, err := GetDiskUsage()
	if err != nil {
		return fmt.Sprintf("⚠️ disk read error: %v", err)
	}
	if len(usages) == 0 {
		return "_(no real filesystems detected)_"
	}

	var sb strings.Builder
	sb.WriteString("💾 **DISK USAGE**\n\n")
	for _, u := range usages {
		emoji := "🟢"
		if u.UsedPct >= 90 {
			emoji = "🔴"
		} else if u.UsedPct >= 75 {
			emoji = "🟡"
		}
		sb.WriteString(fmt.Sprintf("%s **%s** (%s)\n", emoji, u.Mount, u.Filesystem))
		sb.WriteString(fmt.Sprintf("   `[%-20s]` %.1f%% (%.1f / %.1f GB)\n\n",
			asciiBar(u.UsedPct), u.UsedPct, u.UsedGB, u.TotalGB))
	}
	return sb.String()
}

// CheckDiskAlerts returns mountpoints where usage has crossed the threshold.
// Default threshold (85%) catches the common "disk near full" outage class
// while leaving some headroom for log rotation, image builds, etc.
const defaultDiskAlertThreshold = 85.0

// DiskAlert names a mountpoint that has exceeded the threshold.
type DiskAlert struct {
	Mount   string
	UsedPct float64
	UsedGB  float64
	TotalGB float64
}

// CheckDiskAlerts walks every real filesystem and returns those at or above
// the threshold. Caller decides what to do (Telegram, audit, autopilot).
func CheckDiskAlerts(thresholdPct float64) ([]DiskAlert, error) {
	if thresholdPct <= 0 {
		thresholdPct = defaultDiskAlertThreshold
	}
	usages, err := GetDiskUsage()
	if err != nil {
		return nil, err
	}
	var alerts []DiskAlert
	for _, u := range usages {
		if u.UsedPct >= thresholdPct {
			alerts = append(alerts, DiskAlert{
				Mount:   u.Mount,
				UsedPct: u.UsedPct,
				UsedGB:  u.UsedGB,
				TotalGB: u.TotalGB,
			})
		}
	}
	return alerts, nil
}
