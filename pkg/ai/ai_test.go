package ai

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestExtractIntents_AllActions covers every action regex in utils.go to
// prevent regressions when new actions are wired up.
func TestExtractIntents_AllActions(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantAction string
		wantTarget string
	}{
		{"stop", `StopContainer("redis1")`, "StopContainer", "redis1"},
		{"shutdown alias", `ShutdownContainer(nginx)`, "StopContainer", "nginx"},
		{"start", `StartContainer("api1")`, "StartContainer", "api1"},
		{"restart", `RestartContainer(db1)`, "RestartContainer", "db1"},
		{"logs", `ViewLogs("worker")`, "ViewLogs", "worker"},
		{"terraform", `TerraformApply()`, "TerraformApply", ""},
		{"audit", `AuditSecurity()`, "AuditSecurity", ""},
		{"visual", `VisualMetrics()`, "VisualMetrics", ""},
		{"clear cache", `ClearCache()`, "ClearCache", ""},
		{"container stats", `ContainerStats()`, "ContainerStats", ""},
		{"disk usage", `DiskUsage()`, "DiskUsage", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			intents := ExtractIntents(c.output)
			if len(intents) != 1 {
				t.Fatalf("expected 1 intent, got %d: %+v", len(intents), intents)
			}
			if intents[0].Action != c.wantAction {
				t.Errorf("action = %q, want %q", intents[0].Action, c.wantAction)
			}
			if intents[0].Target != c.wantTarget {
				t.Errorf("target = %q, want %q", intents[0].Target, c.wantTarget)
			}
		})
	}
}

// TestExtractIntents_MultipleInOneMessage verifies the extractor finds every
// action when an LLM response chains several together.
func TestExtractIntents_MultipleInOneMessage(t *testing.T) {
	out := `First we StopContainer("a") then RestartContainer("b") and finally check VisualMetrics().`
	intents := ExtractIntents(out)
	if len(intents) != 3 {
		t.Fatalf("expected 3 intents, got %d: %+v", len(intents), intents)
	}
}

// TestExtractIntents_NoMatch returns nil/empty when no patterns hit.
func TestExtractIntents_NoMatch(t *testing.T) {
	intents := ExtractIntents("Just a normal response with no tool calls.")
	if len(intents) != 0 {
		t.Errorf("expected 0 intents, got %d", len(intents))
	}
}

// TestLoadEnvConfig_Overrides asserts env vars take effect after LoadEnvConfig
// is called — this was the bug that motivated extracting config.go from init.
func TestLoadEnvConfig_Overrides(t *testing.T) {
	// Save originals so other tests aren't poisoned.
	origTimeout, origAttempts, origDelay := aiTimeout, aiRetryAttempts, aiRetryBaseDelay
	t.Cleanup(func() {
		aiTimeout = origTimeout
		aiRetryAttempts = origAttempts
		aiRetryBaseDelay = origDelay
	})

	t.Setenv("AI_TIMEOUT_SECONDS", "42")
	t.Setenv("AI_RETRY_ATTEMPTS", "7")
	t.Setenv("AI_RETRY_BASE_DELAY_MS", "250")
	LoadEnvConfig()

	if aiTimeout != 42*time.Second {
		t.Errorf("aiTimeout = %v, want 42s", aiTimeout)
	}
	if aiRetryAttempts != 7 {
		t.Errorf("aiRetryAttempts = %d, want 7", aiRetryAttempts)
	}
	if aiRetryBaseDelay != 250*time.Millisecond {
		t.Errorf("aiRetryBaseDelay = %v, want 250ms", aiRetryBaseDelay)
	}
}

// TestLoadEnvConfig_IgnoresGarbage asserts non-numeric env values are silently
// rejected rather than crashing or zeroing the knobs.
func TestLoadEnvConfig_IgnoresGarbage(t *testing.T) {
	orig := aiTimeout
	t.Cleanup(func() { aiTimeout = orig })

	t.Setenv("AI_TIMEOUT_SECONDS", "not-a-number")
	LoadEnvConfig()
	if aiTimeout != orig {
		t.Errorf("garbage env clobbered aiTimeout: got %v, want %v", aiTimeout, orig)
	}
}

// TestDestructiveAllowed_OnlyTrueEnablesIt pins the safety gate: any value
// other than the literal "true" leaves destructive tools disabled.
func TestDestructiveAllowed_OnlyTrueEnablesIt(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"True":  false,
		"TRUE":  false,
		"1":     false,
		"yes":   false,
		"":      false,
		"false": false,
	}
	for v, want := range cases {
		if v == "" {
			os.Unsetenv("AI_DESTRUCTIVE_ALLOWED")
		} else {
			t.Setenv("AI_DESTRUCTIVE_ALLOWED", v)
		}
		got := destructiveAllowed()
		if got != want {
			t.Errorf("AI_DESTRUCTIVE_ALLOWED=%q → %v, want %v", v, got, want)
		}
	}
}

// TestWithRetry_SuccessFirstTry exits immediately on success without sleeping.
func TestWithRetry_SuccessFirstTry(t *testing.T) {
	origAttempts := aiRetryAttempts
	aiRetryAttempts = 3
	t.Cleanup(func() { aiRetryAttempts = origAttempts })

	var calls int32
	err := withRetry(context.Background(), func() error {
		atomic.AddInt32(&calls, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// TestWithRetry_RetriesThenSucceeds verifies the loop retries up to the cap.
func TestWithRetry_RetriesThenSucceeds(t *testing.T) {
	origAttempts, origDelay := aiRetryAttempts, aiRetryBaseDelay
	aiRetryAttempts = 5
	aiRetryBaseDelay = 1 * time.Millisecond
	t.Cleanup(func() {
		aiRetryAttempts = origAttempts
		aiRetryBaseDelay = origDelay
	})

	var calls int32
	err := withRetry(context.Background(), func() error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// TestWithRetry_AbortsOnContextCancel — a cancelled context must short-circuit
// the retry loop instead of running through every attempt.
func TestWithRetry_AbortsOnContextCancel(t *testing.T) {
	origAttempts, origDelay := aiRetryAttempts, aiRetryBaseDelay
	aiRetryAttempts = 10
	aiRetryBaseDelay = 100 * time.Millisecond
	t.Cleanup(func() {
		aiRetryAttempts = origAttempts
		aiRetryBaseDelay = origDelay
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done before first attempt

	err := withRetry(ctx, func() error {
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestWithRetry_ExhaustsAndReturnsLastError after every attempt fails.
func TestWithRetry_ExhaustsAndReturnsLastError(t *testing.T) {
	origAttempts, origDelay := aiRetryAttempts, aiRetryBaseDelay
	aiRetryAttempts = 3
	aiRetryBaseDelay = 1 * time.Millisecond
	t.Cleanup(func() {
		aiRetryAttempts = origAttempts
		aiRetryBaseDelay = origDelay
	})

	wantErr := errors.New("final boss")
	var calls int32
	err := withRetry(context.Background(), func() error {
		atomic.AddInt32(&calls, 1)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}
