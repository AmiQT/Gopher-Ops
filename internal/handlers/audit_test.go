package handlers

import (
	"strings"
	"testing"
)

// Parser tests for /audit. These cover the daily SRE flow of "operator types
// a slightly-wrong command in chat and expects forgiving behavior" — we want
// the bot to return help rather than crash or silently misinterpret.

func TestAuditCommand_EmptyShowsHelp(t *testing.T) {
	out := AuditCommand("")
	if !strings.Contains(out, "AUDIT COMMANDS") {
		t.Errorf("empty args should show help, got: %s", out)
	}
}

func TestAuditCommand_UnknownSubcommandShowsHelp(t *testing.T) {
	out := AuditCommand("foobar")
	if !strings.Contains(out, "AUDIT COMMANDS") {
		t.Errorf("unknown subcommand should fall back to help, got: %s", out)
	}
}

func TestAuditCommand_SearchWithoutQuery(t *testing.T) {
	out := AuditCommand("search")
	if !strings.Contains(out, "Usage") {
		t.Errorf("search without query should show usage hint, got: %s", out)
	}
}

func TestAuditCommand_WhoWithoutActor(t *testing.T) {
	out := AuditCommand("who")
	if !strings.Contains(out, "Usage") {
		t.Errorf("who without actor should show usage hint, got: %s", out)
	}
}

func TestParseLimit_DefaultsWhenEmpty(t *testing.T) {
	if got := parseLimit(nil, 20); got != 20 {
		t.Errorf("empty args should yield default, got %d", got)
	}
}

func TestParseLimit_DefaultsOnNegative(t *testing.T) {
	// Operators typo "/audit recent -5". We want default, not crash, not -5.
	if got := parseLimit([]string{"-5"}, 20); got != 20 {
		t.Errorf("negative limit should fall back to default, got %d", got)
	}
}

func TestParseLimit_DefaultsOnHuge(t *testing.T) {
	// "/audit recent 9999" — we cap at 100 to prevent rendering 9999 lines
	// in Telegram (which would error out anyway).
	if got := parseLimit([]string{"9999"}, 20); got != 20 {
		t.Errorf("huge limit should fall back to default, got %d", got)
	}
}

func TestParseLimit_DefaultsOnNonNumeric(t *testing.T) {
	if got := parseLimit([]string{"abc"}, 20); got != 20 {
		t.Errorf("non-numeric should yield default, got %d", got)
	}
}

func TestParseLimit_AcceptsValid(t *testing.T) {
	if got := parseLimit([]string{"5"}, 20); got != 5 {
		t.Errorf("valid limit should be honored, got %d", got)
	}
}

func TestParseSearchArgs_QueryOnly(t *testing.T) {
	q, lim := parseSearchArgs([]string{"redis"})
	if q != "redis" || lim != auditDefaultLimit {
		t.Errorf("single-word query: got (%q, %d)", q, lim)
	}
}

func TestParseSearchArgs_QueryAndLimit(t *testing.T) {
	// "redis 5" — trailing numeric is the limit.
	q, lim := parseSearchArgs([]string{"redis", "5"})
	if q != "redis" || lim != 5 {
		t.Errorf("query+limit split wrong: got (%q, %d)", q, lim)
	}
}

func TestParseSearchArgs_MultiWordQuery(t *testing.T) {
	// "container down 10" — query is "container down", limit is 10.
	// This catches the daily "operator pastes multi-word grep" scenario.
	q, lim := parseSearchArgs([]string{"container", "down", "10"})
	if q != "container down" || lim != 10 {
		t.Errorf("multi-word + limit: got (%q, %d)", q, lim)
	}
}

func TestParseSearchArgs_NumericInQueryNotMistakenForLimit(t *testing.T) {
	// "redis-1" is a container name — must NOT be split as ("redis", limit=1).
	q, lim := parseSearchArgs([]string{"redis-1"})
	if q != "redis-1" || lim != auditDefaultLimit {
		t.Errorf("container-name query mis-parsed: got (%q, %d)", q, lim)
	}
}
