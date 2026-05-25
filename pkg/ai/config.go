package ai

import (
	"os"
	"strconv"
	"time"
)

// Shared runtime knobs for the AI package. Default values are applied at
// package init; LoadEnvConfig overrides them from environment variables.
// Splitting init from load is critical: callers (cmd/main.go) populate the
// process environment from a .env file BEFORE these vars are needed, so we
// must read env lazily — not at package-init time, which fires before
// godotenv.Load() runs.
var (
	aiTimeout        = 60 * time.Second
	aiRetryAttempts  = 3
	aiRetryBaseDelay = 1 * time.Second
)

// LoadEnvConfig reads AI-related environment variables into the package
// globals. Call exactly once from main(), after godotenv.Load().
func LoadEnvConfig() {
	if v := os.Getenv("AI_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			aiTimeout = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("AI_RETRY_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			aiRetryAttempts = n
		}
	}
	if v := os.Getenv("AI_RETRY_BASE_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			aiRetryBaseDelay = time.Duration(n) * time.Millisecond
		}
	}
}

// destructiveAllowed gates whether the AI can directly execute Stop/Restart.
// Read on every call so a runtime toggle in the .env (re-source + restart) is
// honored.
func destructiveAllowed() bool {
	return os.Getenv("AI_DESTRUCTIVE_ALLOWED") == "true"
}
