package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// IncidentReport defines the structured data model for SRE post-mortems.
type IncidentReport struct {
	IncidentID     string    `json:"incident_id"`
	Title          string    `json:"title"`
	Severity       string    `json:"severity"` // CRITICAL, HIGH, MEDIUM, LOW
	Service        string    `json:"service"`
	DetectedAt     time.Time `json:"detected_at"`
	ResolvedAt     time.Time `json:"resolved_at"`
	Summary        string    `json:"summary"`
	RootCause      string    `json:"root_cause"`
	LogEvidence    string    `json:"log_evidence"`
	ActionTaken    string    `json:"action_taken"`
	PreventionPlan string    `json:"prevention_plan"`
}

// Manager handles saving, loading, and listing SRE post-mortem reports.
type Manager struct {
	dir string
}

// NewManager initializes the post-mortem report manager.
func NewManager(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create reports directory: %w", err)
	}
	return &Manager{dir: dir}, nil
}

// Generate creates a markdown post-mortem report on disk and returns its path.
func (m *Manager) Generate(r IncidentReport) (string, error) {
	if r.IncidentID == "" {
		r.IncidentID = fmt.Sprintf("INC-%s", time.Now().Format("20060102-150405"))
	}
	if r.DetectedAt.IsZero() {
		r.DetectedAt = time.Now()
	}
	if r.ResolvedAt.IsZero() {
		r.ResolvedAt = time.Now()
	}

	duration := r.ResolvedAt.Sub(r.DetectedAt).Round(time.Second)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 📋 SRE Incident Post-Mortem: %s\n\n", r.Title))
	sb.WriteString("```yaml\n")
	sb.WriteString(fmt.Sprintf("incident_id: %q\n", r.IncidentID))
	sb.WriteString(fmt.Sprintf("severity: %q\n", r.Severity))
	sb.WriteString(fmt.Sprintf("service: %q\n", r.Service))
	sb.WriteString(fmt.Sprintf("detected_at: %q\n", r.DetectedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("resolved_at: %q\n", r.ResolvedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("time_to_recovery: %q\n", duration.String()))
	sb.WriteString("```\n\n")

	sb.WriteString("## 1. 🔍 Executive Summary\n")
	sb.WriteString(r.Summary + "\n\n")

	sb.WriteString("## 2. 🧠 Root Cause Analysis (Smoking Gun)\n")
	sb.WriteString(r.RootCause + "\n\n")

	if r.LogEvidence != "" {
		sb.WriteString("### 📜 Log & Diagnostic Evidence\n```text\n")
		sb.WriteString(strings.TrimSpace(r.LogEvidence))
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("## 3. 🛠️ Remediation Actions Taken\n")
	sb.WriteString(r.ActionTaken + "\n\n")

	sb.WriteString("## 4. 🛡️ Preventative Measures & Lessons Learned\n")
	sb.WriteString(r.PreventionPlan + "\n\n")

	sb.WriteString("---\n*Generated autonomously by Gopher-Ops SRE Agent.*\n")

	filename := fmt.Sprintf("%s.md", r.IncidentID)
	fullPath := filepath.Join(m.dir, filename)

	if err := os.WriteFile(fullPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("failed to write post-mortem report: %w", err)
	}

	return fullPath, nil
}

// ListRecent returns a summary string of the most recent post-mortem reports.
func (m *Manager) ListRecent(limit int) (string, error) {
	files, err := os.ReadDir(m.dir)
	if err != nil {
		return "", err
	}

	var mdFiles []string
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
			mdFiles = append(mdFiles, f.Name())
		}
	}

	if len(mdFiles) == 0 {
		return "_(No post-mortem reports found in archive)_", nil
	}

	sort.Sort(sort.Reverse(sort.StringSlice(mdFiles)))

	if limit > 0 && len(mdFiles) > limit {
		mdFiles = mdFiles[:limit]
	}

	var sb strings.Builder
	sb.WriteString("📋 **RECENT SRE POST-MORTEM REPORTS:**\n\n")
	for _, f := range mdFiles {
		sb.WriteString(fmt.Sprintf("• 📄 `%s`\n", f))
	}
	return sb.String(), nil
}
