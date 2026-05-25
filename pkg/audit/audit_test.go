package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetForTest restores package state so tests don't leak between each other.
func resetForTest(t *testing.T, p string) {
	t.Helper()
	mu.Lock()
	path = p
	maxSize = 10 * 1024 * 1024
	maxFiles = 5
	mu.Unlock()
}

// TestLog_WritesJSONLine verifies the basic contract: one Log() call appends
// exactly one valid JSON line containing the event fields.
func TestLog_WritesJSONLine(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)

	Log(Event{
		Type:   "action",
		Actor:  "user",
		Action: "RestartContainer",
		Target: "redis-1",
		Status: "ok",
	})

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var got Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Action != "RestartContainer" || got.Target != "redis-1" || got.Status != "ok" {
		t.Errorf("event mismatch: %+v", got)
	}
	if got.Time.IsZero() {
		t.Errorf("Log should auto-fill Time when zero")
	}
}

// TestLog_AppendsInOrder asserts multiple Log calls all land in the file in
// order — no overwrite, no swallowed writes.
func TestLog_AppendsInOrder(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)

	for i := 0; i < 5; i++ {
		Log(Event{Type: "action", Actor: "system", Action: fmt.Sprintf("evt-%d", i), Status: "ok"})
	}

	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var actions []string
	for scanner.Scan() {
		var e Event
		_ = json.Unmarshal(scanner.Bytes(), &e)
		actions = append(actions, e.Action)
	}
	if len(actions) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(actions))
	}
	for i, a := range actions {
		want := fmt.Sprintf("evt-%d", i)
		if a != want {
			t.Errorf("line %d: got %q, want %q", i, a, want)
		}
	}
}

// TestRotation_TriggersAtThreshold pins the rotation contract: once the file
// exceeds maxSize the active file becomes .1 and writes continue to a fresh file.
// This is critical — silent rotation failure would unboundedly grow audit.log.
func TestRotation_TriggersAtThreshold(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)
	SetRotation(200, 3) // tiny threshold so a handful of events triggers it

	// Write events until the active file rolls.
	for i := 0; i < 20; i++ {
		Log(Event{Type: "action", Actor: "system", Action: "padding", Status: "ok", Detail: strings.Repeat("x", 30)})
	}

	if _, err := os.Stat(logPath + ".1"); os.IsNotExist(err) {
		t.Errorf("expected %s.1 after rotation, but it doesn't exist", logPath)
	}
	// Active file should still exist (the latest write went there).
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("active audit log missing after rotation")
	}
}

// TestSetPath_NoEmpty asserts SetPath silently ignores empty input rather than
// blanking the destination — a partial config load must not break auditing.
func TestSetPath_NoEmpty(t *testing.T) {
	resetForTest(t, "audit.log")
	SetPath("")
	mu.Lock()
	got := path
	mu.Unlock()
	if got != "audit.log" {
		t.Errorf("SetPath(\"\") clobbered path to %q", got)
	}
}
