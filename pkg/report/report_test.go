package report

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostMortemReportManager(t *testing.T) {
	tempDir := t.TempDir()

	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	report := IncidentReport{
		IncidentID:     "INC-20260903-TEST",
		Title:          "Redis High Memory OOM Outage",
		Severity:       "CRITICAL",
		Service:        "redis-cart",
		DetectedAt:     time.Now().Add(-15 * time.Minute),
		ResolvedAt:     time.Now(),
		Summary:        "Cart service suffered 504 timeouts due to Redis container crashlooping from OOM.",
		RootCause:      "Unbounded cache key growth without TTL in release v2.4.1.",
		LogEvidence:    "kernel: Out of memory: Killed process 8421 (redis-server)",
		ActionTaken:    "Scaled container memory to 2GB and flushed expired user sessions.",
		PreventionPlan: "Added maxmemory-policy volatile-lru and alerting threshold at 80%.",
	}

	filePath, err := mgr.Generate(report)
	if err != nil {
		t.Fatalf("failed to generate report: %v", err)
	}

	if !strings.HasSuffix(filePath, "INC-20260903-TEST.md") {
		t.Errorf("unexpected file path: %s", filePath)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read generated report: %v", err)
	}

	if !strings.Contains(string(content), "Redis High Memory OOM Outage") {
		t.Errorf("expected title in report content")
	}

	if !strings.Contains(string(content), "volatile-lru") {
		t.Errorf("expected prevention plan in report content")
	}

	recent, err := mgr.ListRecent(5)
	if err != nil {
		t.Fatalf("failed to list recent reports: %v", err)
	}

	if !strings.Contains(recent, "INC-20260903-TEST.md") {
		t.Errorf("expected report in recent listing, got: %s", recent)
	}
}
