package audit

import (
	"bufio"
	"bytes"
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
	chainHash = "" // reset chain for each test
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

// TestVerify_IntactChain writes several events and asserts Verify() returns
// Broken == -1 (no tamper detected).
func TestVerify_IntactChain(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)

	for i := 0; i < 5; i++ {
		Log(Event{Type: "action", Actor: "system", Action: fmt.Sprintf("evt-%d", i), Status: "ok"})
	}

	result := Verify()
	if result.Broken != -1 {
		t.Errorf("intact chain should have Broken=-1, got %d: %s", result.Broken, result.ErrorMsg)
	}
	if result.Entries != 5 {
		t.Errorf("expected 5 entries, got %d", result.Entries)
	}
}

// TestVerify_DetectsTamper modifies one entry and asserts Verify() catches it.
func TestVerify_DetectsTamper(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)

	for i := 0; i < 3; i++ {
		Log(Event{Type: "action", Actor: "system", Action: fmt.Sprintf("evt-%d", i), Status: "ok"})
	}

	// Read, tamper line 1, rewrite.
	data, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	lines[1] = strings.Replace(lines[1], "evt-1", "TAMPERED", 1)
	_ = os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	result := Verify()
	if result.Broken == -1 {
		t.Error("tampered chain should have been detected")
	}
}

// TestLog_HashFieldPopulated asserts every written entry has a non-empty Hash.
func TestLog_HashFieldPopulated(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	resetForTest(t, logPath)

	Log(Event{Type: "boot", Actor: "system", Status: "ok"})

	data, _ := os.ReadFile(logPath)
	var evt Event
	_ = json.Unmarshal(bytes.TrimRight(data, "\n"), &evt)
	if evt.Hash == "" {
		t.Error("Log() should populate the Hash field")
	}
}
