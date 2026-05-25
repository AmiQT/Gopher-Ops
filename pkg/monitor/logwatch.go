package monitor

import (
	"context"
	"io"
	"regexp"
	"strings"
	"time"

	"gopher-ops/pkg/docker"

	"github.com/docker/docker/api/types/container"
)

// LogPattern is a named regex that signals a known-bad condition in logs.
type LogPattern struct {
	Name    string
	Pattern *regexp.Regexp
	Level   string // "critical" | "warning"
}

// defaultLogPatterns is the set of patterns checked every minute. The list
// covers the most common application failure signatures; operators can extend
// it by calling AddLogPattern() before the monitor starts.
var defaultLogPatterns = []LogPattern{
	{Name: "oom_killer", Pattern: regexp.MustCompile(`(?i)out of memory|OOM killer|killed process`), Level: "critical"},
	{Name: "panic", Pattern: regexp.MustCompile(`(?i)\bpanic\b|SIGSEGV|segmentation fault`), Level: "critical"},
	{Name: "fatal", Pattern: regexp.MustCompile(`(?i)\bFATAL\b|\bfatal error\b`), Level: "critical"},
	{Name: "conn_refused", Pattern: regexp.MustCompile(`(?i)connection refused|dial tcp.*refused`), Level: "warning"},
	{Name: "timeout", Pattern: regexp.MustCompile(`(?i)context deadline exceeded|i\/o timeout|timed out`), Level: "warning"},
	{Name: "disk_full", Pattern: regexp.MustCompile(`(?i)no space left on device|disk full`), Level: "critical"},
	{Name: "permission_denied", Pattern: regexp.MustCompile(`(?i)permission denied|access denied`), Level: "warning"},
}

var extraLogPatterns []LogPattern

// AddLogPattern registers an additional pattern checked during log scanning.
func AddLogPattern(p LogPattern) {
	extraLogPatterns = append(extraLogPatterns, p)
}

// allPatterns returns the combined default + extra patterns.
func allPatterns() []LogPattern {
	all := make([]LogPattern, len(defaultLogPatterns)+len(extraLogPatterns))
	copy(all, defaultLogPatterns)
	copy(all[len(defaultLogPatterns):], extraLogPatterns)
	return all
}

// LogMatch records a single pattern hit in a container's recent logs.
type LogMatch struct {
	ContainerID   string
	ContainerName string
	PatternName   string
	Level         string
	MatchedLine   string
}

// ScanContainerLogs fetches the last `since` duration of logs for all running
// containers and returns every pattern match found. The scan window is kept
// short (default 1 minute) so we only surface new occurrences each tick.
func ScanContainerLogs(ctx context.Context, since time.Duration) ([]LogMatch, error) {
	cli, err := docker.Get()
	if err != nil {
		return nil, err
	}

	listCtx, cancel := context.WithTimeout(ctx, dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(listCtx, container.ListOptions{All: false})
	if err != nil {
		return nil, err
	}

	patterns := allPatterns()
	var matches []LogMatch

	for _, c := range containers {
		name := strings.TrimPrefix(c.Names[0], "/")
		shortID := c.ID[:8]

		logCtx, logCancel := context.WithTimeout(ctx, 5*time.Second)
		sinceStr := time.Now().Add(-since).UTC().Format(time.RFC3339)
		rc, err := cli.ContainerLogs(logCtx, c.ID, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Since:      sinceStr,
			Tail:       "100",
		})
		logCancel()
		if err != nil {
			continue
		}

		raw, _ := io.ReadAll(rc)
		rc.Close()

		// Docker prepends an 8-byte header to each log frame; strip them.
		lines := stripDockerLogHeaders(raw)

		for _, line := range lines {
			for _, p := range patterns {
				if p.Pattern.MatchString(line) {
					// Truncate long lines so Telegram messages stay readable.
					trimmed := line
					if len(trimmed) > 200 {
						trimmed = trimmed[:197] + "..."
					}
					matches = append(matches, LogMatch{
						ContainerID:   shortID,
						ContainerName: name,
						PatternName:   p.Name,
						Level:         p.Level,
						MatchedLine:   strings.TrimSpace(trimmed),
					})
					break // one match per line is enough
				}
			}
		}
	}
	return matches, nil
}

// stripDockerLogHeaders removes the 8-byte multiplexed stream header that the
// Docker API prepends to each log frame (stream type + 3 padding bytes + 4 size bytes).
func stripDockerLogHeaders(raw []byte) []string {
	var lines []string
	for len(raw) > 8 {
		frameSize := int(raw[4])<<24 | int(raw[5])<<16 | int(raw[6])<<8 | int(raw[7])
		raw = raw[8:]
		if frameSize > len(raw) {
			frameSize = len(raw)
		}
		chunk := string(raw[:frameSize])
		raw = raw[frameSize:]
		for _, l := range strings.Split(chunk, "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
	}
	return lines
}
