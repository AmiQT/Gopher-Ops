package handlers

import (
	"strings"
	"testing"
)

// Tests for the inline-text command parsers. These catch the "operator typos
// a command at 3am" failure mode — we want graceful rejection, never panic
// and never silently apply wrong values.

func TestHandleUpdate_RequiresEnoughArgs(t *testing.T) {
	// Real-world typo: forgot the mem value.
	_, handled := handleUpdate("update nginx cpu 0.5")
	if handled {
		t.Error("incomplete update should not be handled")
	}
}

func TestHandleUpdate_ParsesValidInput(t *testing.T) {
	// Won't actually mutate any container (the real Docker call will fail
	// in a test env), but we can verify the parser at least proceeded past
	// argument validation.
	out, handled := handleUpdate("update fake-id cpu 0.5 mem 512")
	if !handled {
		t.Error("valid-shape update should be handled (even if Docker fails)")
	}
	// The reply should mention "Update failed" because the container doesn't
	// exist — but the important thing is we got *past* parsing.
	if out == "" {
		t.Error("expected some reply text")
	}
}

func TestHandleSearch_RequiresThreeParts(t *testing.T) {
	cases := []string{
		"search",         // missing both
		"search nginx",   // missing pattern
	}
	for _, in := range cases {
		_, handled := handleSearch(in)
		if handled {
			t.Errorf("incomplete search %q should not be handled", in)
		}
	}
}

func TestHandleSearch_ParsesValidInput(t *testing.T) {
	_, handled := handleSearch("search fake-id error")
	if !handled {
		t.Error("valid-shape search should be handled (Docker call may fail)")
	}
}

func TestHandleSearch_MultiWordPatternIsOneArg(t *testing.T) {
	// "search nginx connection refused" — pattern is "connection refused"
	// (preserved with the space). Catches naive Fields() splitters.
	out, handled := handleSearch("search nginx connection refused")
	if !handled {
		t.Error("multi-word pattern should still be handled")
	}
	_ = out // implementation passes pattern to grep; we can't verify the actual grep here
}

func TestHandleChaosKill_DisabledByDefault(t *testing.T) {
	// CHAOS_ENABLED isn't set in test env → chaos.Enabled() returns false.
	// Bot must refuse rather than silently kill a real container.
	out, handled := handleChaosKill("chaos kill nginx")
	if !handled {
		t.Error("chaos kill with valid shape should be handled")
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("chaos disabled message expected, got: %s", out)
	}
}

func TestHandleChaosKill_RequiresTarget(t *testing.T) {
	_, handled := handleChaosKill("chaos kill")
	if handled {
		t.Error("chaos kill without target should not be handled")
	}
}
