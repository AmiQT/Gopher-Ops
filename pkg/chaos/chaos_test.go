package chaos

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestEnabled_FalseByDefault verifies chaos is off unless the env var is set.
func TestEnabled_FalseByDefault(t *testing.T) {
	os.Unsetenv("CHAOS_ENABLED")
	if Enabled() {
		t.Error("chaos should be disabled when CHAOS_ENABLED is not set")
	}
}

// TestEnabled_TrueWhenSet verifies the env gate works.
func TestEnabled_TrueWhenSet(t *testing.T) {
	t.Setenv("CHAOS_ENABLED", "true")
	if !Enabled() {
		t.Error("chaos should be enabled when CHAOS_ENABLED=true")
	}
}

// TestEnabled_IgnoresOtherValues ensures only the exact string "true" activates chaos.
func TestEnabled_IgnoresOtherValues(t *testing.T) {
	for _, v := range []string{"1", "yes", "TRUE", "on", ""} {
		t.Setenv("CHAOS_ENABLED", v)
		if Enabled() {
			t.Errorf("CHAOS_ENABLED=%q should not enable chaos", v)
		}
	}
}

// TestKillRandom_DisabledReturnsError asserts that KillRandom refuses when
// chaos mode is off — prevents accidental production kills.
func TestKillRandom_DisabledReturnsError(t *testing.T) {
	os.Unsetenv("CHAOS_ENABLED")
	_, err := KillRandom(context.Background())
	if err == nil {
		t.Fatal("expected error when chaos disabled, got nil")
	}
}

// TestKillNamed_DisabledReturnsError asserts that KillNamed refuses when disabled.
func TestKillNamed_DisabledReturnsError(t *testing.T) {
	os.Unsetenv("CHAOS_ENABLED")
	_, err := KillNamed(context.Background(), "any-container")
	if err == nil {
		t.Fatal("expected error when chaos disabled, got nil")
	}
}

// TestFormatResult_ContainsKeyFields asserts the Telegram summary includes the
// container name, short ID, and stop time.
func TestFormatResult_ContainsKeyFields(t *testing.T) {
	r := &KillResult{
		ContainerID:   "abc12345",
		ContainerName: "redis-cache",
		StoppedAt:     time.Date(2025, 1, 15, 14, 30, 0, 0, time.UTC),
	}
	out := FormatResult(r)
	for _, want := range []string{"redis-cache", "abc12345", "14:30:00", "CHAOS"} {
		if !containsStr(out, want) {
			t.Errorf("FormatResult missing %q in output:\n%s", want, out)
		}
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
