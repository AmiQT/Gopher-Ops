package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

func tempLogFile(t *testing.T) (path string, cleanup func()) {
	t.Helper()
	f, err := os.CreateTemp("", "audit_test_*.jsonl")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }
}

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open audit file: %v", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("invalid JSON line: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestLog_WritesEntry(t *testing.T) {
	path, cleanup := tempLogFile(t)
	defer cleanup()

	orig := logFile
	// Override package-level constant via a variable swap trick
	// (we patch it directly since logFile is a const — use the real file for this test)
	_ = orig

	// Use the real logFile path by patching within the test
	// Since logFile is a const, we test indirectly by calling Log and reading audit.jsonl
	// We redirect by temporarily changing working dir to a temp dir
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	Log("autopilot", "RestartContainer", "my-app", "attempt 1")

	entries := readEntries(t, tmpDir+"/audit.jsonl")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Trigger != "autopilot" {
		t.Errorf("expected trigger=autopilot, got %q", e.Trigger)
	}
	if e.Action != "RestartContainer" {
		t.Errorf("expected action=RestartContainer, got %q", e.Action)
	}
	if e.Target != "my-app" {
		t.Errorf("expected target=my-app, got %q", e.Target)
	}
	if e.Result != "attempt 1" {
		t.Errorf("expected result='attempt 1', got %q", e.Result)
	}
	if e.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}

	_ = path
}

func TestLog_AppendsMultipleEntries(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	Log("autopilot", "RestartContainer", "svc-a", "attempt 1")
	Log("manual", "Alert", "svc-b", "sent")
	Log("escalation", "EscalationAlert", "svc-c", "failed after 3 attempts")

	entries := readEntries(t, tmpDir+"/audit.jsonl")
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Action != "RestartContainer" {
		t.Errorf("wrong first entry action: %q", entries[0].Action)
	}
	if entries[2].Trigger != "escalation" {
		t.Errorf("wrong third entry trigger: %q", entries[2].Trigger)
	}
}

func TestLog_ValidTimestampFormat(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	Log("manual", "Silence", "autopilot", "30m")

	entries := readEntries(t, tmpDir+"/audit.jsonl")
	if len(entries) == 0 {
		t.Fatal("no entries written")
	}
	ts := entries[0].Timestamp
	if !strings.Contains(ts, "T") || !strings.Contains(ts, ":") {
		t.Errorf("timestamp doesn't look like RFC3339: %q", ts)
	}
}

func TestLog_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			Log("autopilot", "RestartContainer", "svc", "concurrent")
		}(i)
	}
	wg.Wait()

	entries := readEntries(t, tmpDir+"/audit.jsonl")
	if len(entries) != 20 {
		t.Errorf("expected 20 entries from concurrent writes, got %d", len(entries))
	}
}

func TestReadLast_ReturnsNEntries(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	for i := 0; i < 15; i++ {
		Log("autopilot", "RestartContainer", "svc", "ok")
	}

	last := ReadLast(10)
	if len(last) != 10 {
		t.Errorf("expected 10 entries from ReadLast(10), got %d", len(last))
	}
}

func TestReadLast_FewerThanN(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	Log("manual", "Silence", "autopilot", "30m")
	Log("manual", "Silence", "autopilot", "cancelled")

	last := ReadLast(10)
	if len(last) != 2 {
		t.Errorf("expected 2 entries (all of them) from ReadLast(10), got %d", len(last))
	}
}

func TestReadLast_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	last := ReadLast(10)
	if last != nil && len(last) != 0 {
		t.Errorf("expected nil/empty from ReadLast on missing file, got %v", last)
	}
}

func TestRotate_TriggersWhenOverLimit(t *testing.T) {
	tmpDir := t.TempDir()
	origWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origWD)

	// Write a file that exceeds maxBytes
	bigData := make([]byte, maxBytes+1)
	os.WriteFile(logFile, bigData, 0644)

	// Log one more entry — should trigger rotation
	Log("manual", "Test", "target", "result")

	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		t.Error("expected backup file to exist after rotation")
	}
	// New logFile should be small (just the new entry)
	info, _ := os.Stat(logFile)
	if info.Size() >= maxBytes {
		t.Errorf("expected new logFile to be small after rotation, got %d bytes", info.Size())
	}
}
