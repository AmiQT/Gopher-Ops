package actions

import (
	"strings"
	"testing"
)

// TestClearCachePlaceholder pins the deliberately-honest placeholder string.
// ClearCache() is intentionally unimplemented; the response must clearly say
// so rather than claim success.
func TestClearCachePlaceholder(t *testing.T) {
	result := ClearCache()
	if !strings.Contains(result, "not implemented") {
		t.Errorf("ClearCache should declare itself unimplemented; got: %q", result)
	}
}

// TestRedactSecret ensures credential-like env vars are masked before being
// shipped to the AI or rendered in Telegram chat.
func TestRedactSecret(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"DB_PASSWORD=hunter2", "DB_PASSWORD=***REDACTED***"},
		{"API_TOKEN=abc123", "API_TOKEN=***REDACTED***"},
		{"SECRET_KEY=xyz", "SECRET_KEY=***REDACTED***"},
		{"GOOGLE_API_KEY=foo", "GOOGLE_API_KEY=***REDACTED***"},
		{"PORT=8080", "PORT=8080"},
		{"LOG_LEVEL=info", "LOG_LEVEL=info"},
		{"NO_EQUALS_SIGN", "NO_EQUALS_SIGN"},
	}
	for _, c := range cases {
		got := redactSecret(c.in)
		if got != c.want {
			t.Errorf("redactSecret(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
