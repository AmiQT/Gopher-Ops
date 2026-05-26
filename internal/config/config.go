// Package config centralizes every env-driven knob the bot reads at startup.
//
// Before this package, env lookups were scattered across main.go, monitor, ai,
// audit, etc. — making it hard to know what's configurable, what the defaults
// are, or to validate everything up front. Now: one struct, one Load() call,
// every default in one place.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the validated, fully-resolved runtime configuration. Every field
// is populated by Load() — callers should never read os.Getenv themselves.
type Config struct {
	// Credentials
	GeminiAPIKey  string
	TelegramToken string

	// Authorization
	AuthorizedChatIDs []int64
	AuthorizedSet     map[int64]bool

	// LLM
	LLMProvider     string
	LLMBaseURL      string
	LLMModel        string
	LocalLLMAPIKey  string
	AIDestructiveOK bool

	// Monitoring thresholds
	CPUThreshold      float64
	CPUDurationMin    int
	AlertCooldown     time.Duration
	DiskAlertThresh   float64
	MemPressureThresh float64
	MetricsHistory    int

	// Operational
	AutopilotEnabled bool
	RCALogLines      int
	RateLimitPerMin  float64
	TelegramQueue    int

	// Endpoints
	HealthPort  string
	PprofEnable bool
	MonitorURLs []string

	// Audit
	AuditLogPath     string
	AuditLogMaxBytes int64
	AuditLogMaxFiles int
}

// Load reads every supported environment variable, applies defaults, and
// returns a fully validated Config. Any required-but-missing or malformed
// value returns an error — fail fast at boot rather than panicking later.
func Load() (*Config, error) {
	c := &Config{
		GeminiAPIKey:      os.Getenv("GEMINI_API_KEY"),
		TelegramToken:     os.Getenv("TELEGRAM_BOT_TOKEN"),
		LLMProvider:       envOr("LLM_PROVIDER", "gemini"),
		LLMBaseURL:        os.Getenv("LLM_BASE_URL"),
		LLMModel:          os.Getenv("LLM_MODEL"),
		LocalLLMAPIKey:    envOr("LOCAL_LLM_API_KEY", "lm-studio"),
		AIDestructiveOK:   boolEnv("AI_DESTRUCTIVE_ALLOWED", false),
		CPUThreshold:      floatEnv("ALERT_CPU_THRESHOLD", 80.0),
		CPUDurationMin:    intEnv("ALERT_CPU_DURATION", 5),
		AlertCooldown:     time.Duration(intEnv("ALERT_COOLDOWN_MINUTES", 15)) * time.Minute,
		DiskAlertThresh:   floatEnv("DISK_ALERT_THRESHOLD", 85.0),
		MemPressureThresh: floatEnv("MEM_PRESSURE_THRESHOLD", 90.0),
		MetricsHistory:    intEnv("METRICS_HISTORY_SIZE", 60),
		AutopilotEnabled:  boolEnv("AUTOPILOT_ENABLED", false),
		RCALogLines:       intEnv("RCA_LOG_LINES", 200),
		RateLimitPerMin:   floatEnv("RATE_LIMIT_PER_MINUTE", 30.0),
		TelegramQueue:     intEnv("TELEGRAM_QUEUE_SIZE", 256),
		HealthPort:        os.Getenv("HEALTH_PORT"),
		PprofEnable:       boolEnv("PPROF_ENABLED", false),
		AuditLogPath:      envOr("AUDIT_LOG_PATH", "audit.log"),
		AuditLogMaxBytes:  int64(intEnv("AUDIT_LOG_MAX_SIZE_MB", 10)) * 1024 * 1024,
		AuditLogMaxFiles:  intEnv("AUDIT_LOG_MAX_FILES", 5),
	}

	if urls := os.Getenv("MONITOR_URLS"); urls != "" {
		for _, u := range strings.Split(urls, ",") {
			if s := strings.TrimSpace(u); s != "" {
				c.MonitorURLs = append(c.MonitorURLs, s)
			}
		}
	}

	ids, set, err := parseAuthorizedChatIDs()
	if err != nil {
		return nil, fmt.Errorf("authorized chat ids: %w", err)
	}
	c.AuthorizedChatIDs = ids
	c.AuthorizedSet = set

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate enforces the invariants the rest of the app relies on. Anything
// fatal-at-boot belongs here, not deep in handlers.
func (c *Config) Validate() error {
	if c.GeminiAPIKey == "" {
		return fmt.Errorf("%w: GEMINI_API_KEY", ErrMissingRequired)
	}
	if c.TelegramToken == "" {
		return fmt.Errorf("%w: TELEGRAM_BOT_TOKEN", ErrMissingRequired)
	}
	if len(c.AuthorizedChatIDs) == 0 {
		return fmt.Errorf("%w: AUTHORIZED_CHAT_IDS (or AUTHORIZED_CHAT_ID) — refusing to boot with open access", ErrMissingRequired)
	}
	if c.CPUThreshold <= 0 || c.CPUThreshold > 100 {
		return fmt.Errorf("%w: ALERT_CPU_THRESHOLD must be in (0,100], got %v", ErrOutOfRange, c.CPUThreshold)
	}
	if c.RateLimitPerMin <= 0 {
		return fmt.Errorf("%w: RATE_LIMIT_PER_MINUTE must be > 0, got %v", ErrOutOfRange, c.RateLimitPerMin)
	}
	return nil
}

// LLMAPIKey returns the right key for the configured provider. Gemini uses
// GeminiAPIKey; local uses LocalLLMAPIKey.
func (c *Config) LLMAPIKey() string {
	if c.LLMProvider == "local" {
		return c.LocalLLMAPIKey
	}
	return c.GeminiAPIKey
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func floatEnv(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return fallback
}

// parseAuthorizedChatIDs accepts a comma-separated AUTHORIZED_CHAT_IDS env
// (preferred), or a single AUTHORIZED_CHAT_ID for backwards compatibility,
// and returns the list plus a set for O(1) lookup.
func parseAuthorizedChatIDs() ([]int64, map[int64]bool, error) {
	raw := os.Getenv("AUTHORIZED_CHAT_IDS")
	if raw == "" {
		raw = os.Getenv("AUTHORIZED_CHAT_ID")
	}
	if raw == "" {
		return nil, nil, nil
	}

	var ids []int64
	set := make(map[int64]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: invalid chat ID %q", ErrMalformed, part)
		}
		if !set[id] {
			set[id] = true
			ids = append(ids, id)
		}
	}
	return ids, set, nil
}
