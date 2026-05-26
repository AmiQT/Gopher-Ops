package handlers

import (
	"strings"
	"testing"

	"gopher-ops/internal/snooze"
)

// These tests pin down the snooze callback parser. The trickiest real-world
// case is keys that themselves contain colons — e.g. log alerts use
// "log:<containerID>:<patternName>", so the full callback data looks like
// "Snooze:log:abc123:OOMKill:60". Naive Split would corrupt the key.

func setupSnoozeTest(t *testing.T) {
	t.Helper()
	snooze.Path = t.TempDir() + "/snooze.json"
}

func noopAudit(string, string, error) {}

func TestHandleSnooze_SimpleKey(t *testing.T) {
	setupSnoozeTest(t)
	out := handleSnooze("ctr:abc123:60", noopAudit)

	if !strings.Contains(out, "1 jam") {
		t.Errorf("expected '1 jam' in reply, got: %s", out)
	}
	if !snooze.Active("ctr:abc123") {
		t.Error("snooze did not activate for ctr:abc123")
	}
}

func TestHandleSnooze_KeyWithMultipleColons(t *testing.T) {
	setupSnoozeTest(t)
	// This is the real-world case: a log-pattern alert key already has
	// colons in it. Whole payload after "Snooze:" is "log:abc:OOMKill:1440".
	out := handleSnooze("log:abc:OOMKill:1440", noopAudit)

	if strings.Contains(out, "rosak") || strings.Contains(out, "tak valid") {
		t.Errorf("expected successful parse, got error: %s", out)
	}
	if !snooze.Active("log:abc:OOMKill") {
		t.Error("snooze did not activate for compound key log:abc:OOMKill")
	}
	// And the wrong-but-similar key should NOT be active.
	if snooze.Active("log:abc:OOMKill:1440") {
		t.Error("snooze activated under the wrong (un-split) key")
	}
}

func TestHandleSnooze_RejectsInvalidDuration(t *testing.T) {
	setupSnoozeTest(t)
	cases := []string{
		"ctr:abc:notanumber",
		"ctr:abc:0",
		"ctr:abc:-30",
	}
	for _, in := range cases {
		out := handleSnooze(in, noopAudit)
		if !strings.Contains(out, "tak valid") {
			t.Errorf("expected invalid-duration message for %q, got: %s", in, out)
		}
	}
}

func TestHandleSnooze_RejectsMissingColon(t *testing.T) {
	setupSnoozeTest(t)
	out := handleSnooze("nocolonhere", noopAudit)
	if !strings.Contains(out, "rosak") {
		t.Errorf("expected 'rosak' for missing colon, got: %s", out)
	}
}

func TestHandleSnooze_AllThreePresetDurations(t *testing.T) {
	setupSnoozeTest(t)
	cases := map[int]string{
		60:   "1 jam",
		240:  "4 jam",
		1440: "24 jam",
	}
	for minutes, want := range cases {
		out := handleSnooze("ctr:x:"+itoa(minutes), noopAudit)
		if !strings.Contains(out, want) {
			t.Errorf("for %d minutes, expected %q in output, got: %s", minutes, want, out)
		}
		snooze.Clear("ctr:x")
	}
}

// itoa avoids the strconv import here; the test only uses three known values.
func itoa(n int) string {
	switch n {
	case 60:
		return "60"
	case 240:
		return "240"
	case 1440:
		return "1440"
	}
	return "0"
}
