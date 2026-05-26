package config

import (
	"strings"
	"testing"
)

func TestLoad_RequiredFieldsMissing(t *testing.T) {
	clearEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("expected error when GEMINI_API_KEY missing")
	}
}

func TestLoad_HappyPath(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("AUTHORIZED_CHAT_IDS", "111,222,333")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AuthorizedChatIDs) != 3 {
		t.Errorf("want 3 chat IDs, got %d", len(cfg.AuthorizedChatIDs))
	}
	if !cfg.AuthorizedSet[222] {
		t.Errorf("set lookup broken")
	}
	if cfg.CPUThreshold != 80.0 {
		t.Errorf("default CPU threshold not applied: %v", cfg.CPUThreshold)
	}
	if cfg.LLMProvider != "gemini" {
		t.Errorf("default provider should be gemini, got %s", cfg.LLMProvider)
	}
}

func TestLoad_LegacySingleChatID(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("AUTHORIZED_CHAT_ID", "999")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AuthorizedChatIDs) != 1 || cfg.AuthorizedChatIDs[0] != 999 {
		t.Errorf("legacy single-ID fallback broken: %v", cfg.AuthorizedChatIDs)
	}
}

func TestLoad_InvalidChatID(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("AUTHORIZED_CHAT_IDS", "notanumber")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "invalid chat ID") {
		t.Errorf("expected invalid chat ID error, got %v", err)
	}
}

func TestLoad_OverridesAndDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("AUTHORIZED_CHAT_IDS", "1")
	t.Setenv("ALERT_CPU_THRESHOLD", "95.5")
	t.Setenv("AUTOPILOT_ENABLED", "true")
	t.Setenv("MONITOR_URLS", "https://a.test, https://b.test")
	t.Setenv("RATE_LIMIT_PER_MINUTE", "10")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CPUThreshold != 95.5 {
		t.Errorf("override not applied: %v", cfg.CPUThreshold)
	}
	if !cfg.AutopilotEnabled {
		t.Error("AUTOPILOT_ENABLED=true not parsed")
	}
	if len(cfg.MonitorURLs) != 2 || cfg.MonitorURLs[1] != "https://b.test" {
		t.Errorf("MONITOR_URLS split broken: %v", cfg.MonitorURLs)
	}
	if cfg.RateLimitPerMin != 10 {
		t.Errorf("rate limit override broken: %v", cfg.RateLimitPerMin)
	}
}

func TestLoad_RejectsOutOfRangeCPUThreshold(t *testing.T) {
	clearEnv(t)
	t.Setenv("GEMINI_API_KEY", "k")
	t.Setenv("TELEGRAM_BOT_TOKEN", "t")
	t.Setenv("AUTHORIZED_CHAT_IDS", "1")
	t.Setenv("ALERT_CPU_THRESHOLD", "150")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ALERT_CPU_THRESHOLD") {
		t.Errorf("expected range error, got %v", err)
	}
}

// FuzzParseAuthorizedChatIDs must never panic, even on adversarial input.
func FuzzParseAuthorizedChatIDs(f *testing.F) {
	seeds := []string{"", "1", "1,2,3", " ", ",,", "abc", "1, 2 ,3", "-1", "9223372036854775807"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		t.Setenv("AUTHORIZED_CHAT_IDS", raw)
		_, _, _ = parseAuthorizedChatIDs() // contract: never panic
	})
}

// clearEnv unsets every env var Load() reads, so each test starts clean.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GEMINI_API_KEY", "TELEGRAM_BOT_TOKEN",
		"AUTHORIZED_CHAT_IDS", "AUTHORIZED_CHAT_ID",
		"LLM_PROVIDER", "LLM_BASE_URL", "LLM_MODEL", "LOCAL_LLM_API_KEY",
		"AI_DESTRUCTIVE_ALLOWED", "ALERT_CPU_THRESHOLD", "ALERT_CPU_DURATION",
		"ALERT_COOLDOWN_MINUTES", "DISK_ALERT_THRESHOLD", "MEM_PRESSURE_THRESHOLD",
		"METRICS_HISTORY_SIZE", "AUTOPILOT_ENABLED", "RCA_LOG_LINES",
		"RATE_LIMIT_PER_MINUTE", "TELEGRAM_QUEUE_SIZE", "HEALTH_PORT", "PPROF_ENABLED",
		"MONITOR_URLS", "AUDIT_LOG_PATH", "AUDIT_LOG_MAX_SIZE_MB", "AUDIT_LOG_MAX_FILES",
	} {
		t.Setenv(k, "")
	}
}
