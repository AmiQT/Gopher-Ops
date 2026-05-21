package ai

import (
	"strings"
	"testing"
)

func TestRCAResult_Format_ContainsAllFields(t *testing.T) {
	r := RCAResult{
		RootCause:         "PostgreSQL connection pool exhausted",
		Severity:          "HIGH",
		RecommendedAction: "Tambah max_connections dalam postgresql.conf dan restart",
		Confidence:        "HIGH",
		Summary:           "API server crash sebab database connection habis",
	}

	out := r.Format()

	for _, expected := range []string{
		r.RootCause,
		r.Severity,
		r.RecommendedAction,
		r.Confidence,
		r.Summary,
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected output to contain %q\ngot:\n%s", expected, out)
		}
	}
}

func TestRCAResult_Format_SeverityEmojis(t *testing.T) {
	cases := []struct {
		severity string
		emoji    string
	}{
		{"LOW", "🟢"},
		{"MEDIUM", "🟡"},
		{"HIGH", "🟠"},
		{"CRITICAL", "🔴"},
	}

	for _, tc := range cases {
		r := RCAResult{Severity: tc.severity, Confidence: "HIGH"}
		out := r.Format()
		if !strings.Contains(out, tc.emoji) {
			t.Errorf("severity %q: expected emoji %q in output:\n%s", tc.severity, tc.emoji, out)
		}
	}
}

func TestRCAResult_Format_ConfidenceEmojis(t *testing.T) {
	cases := []struct {
		confidence string
		emoji      string
	}{
		{"LOW", "🤔"},
		{"MEDIUM", "🧐"},
		{"HIGH", "✅"},
	}

	for _, tc := range cases {
		r := RCAResult{Severity: "HIGH", Confidence: tc.confidence}
		out := r.Format()
		if !strings.Contains(out, tc.emoji) {
			t.Errorf("confidence %q: expected emoji %q in output:\n%s", tc.confidence, tc.emoji, out)
		}
	}
}

func TestRCAResult_Format_UnknownSeverityFallback(t *testing.T) {
	r := RCAResult{Severity: "UNKNOWN", Confidence: "UNKNOWN"}
	out := r.Format()
	// Should not panic — fallback emoji ⚪ used
	if !strings.Contains(out, "⚪") {
		t.Errorf("expected fallback emoji ⚪ for unknown severity, got:\n%s", out)
	}
}

func TestRCAResult_Format_TelegramMarkdown(t *testing.T) {
	r := RCAResult{
		RootCause:         "OOM kill",
		Severity:          "CRITICAL",
		RecommendedAction: "Increase memory limit",
		Confidence:        "HIGH",
		Summary:           "Container killed by kernel",
	}
	out := r.Format()
	// Verify bold markdown headers are present for Telegram
	if !strings.Contains(out, "**Punca:**") {
		t.Errorf("expected **Punca:** markdown header, got:\n%s", out)
	}
	if !strings.Contains(out, "**Severity:**") {
		t.Errorf("expected **Severity:** markdown header, got:\n%s", out)
	}
	if !strings.Contains(out, "**Tindakan:**") {
		t.Errorf("expected **Tindakan:** markdown header, got:\n%s", out)
	}
}
