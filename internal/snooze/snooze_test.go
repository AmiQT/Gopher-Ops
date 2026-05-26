package snooze

import (
	"testing"
	"time"
)

func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	until = map[string]time.Time{}
	mu.Unlock()
}

func TestSet_ActivateAndExpire(t *testing.T) {
	reset(t)
	Path = t.TempDir() + "/snooze.json"

	// Use a generous activation window so slow-CI file writes can't beat us.
	Set("disk:/", 500*time.Millisecond)
	if !Active("disk:/") {
		t.Fatal("expected active immediately after Set")
	}
	time.Sleep(600 * time.Millisecond)
	if Active("disk:/") {
		t.Fatal("expected expired after deadline")
	}
}

func TestSet_ZeroDurationIsNoop(t *testing.T) {
	reset(t)
	Path = t.TempDir() + "/snooze.json"

	Set("disk:/", 0)
	if Active("disk:/") {
		t.Error("zero duration should not snooze")
	}
}

func TestClear(t *testing.T) {
	reset(t)
	Path = t.TempDir() + "/snooze.json"

	Set("k", time.Hour)
	if !Active("k") {
		t.Fatal("expected active")
	}
	Clear("k")
	if Active("k") {
		t.Error("Clear did not deactivate")
	}
}

func TestUntil(t *testing.T) {
	reset(t)
	Path = t.TempDir() + "/snooze.json"

	before := time.Now()
	Set("k", time.Hour)
	got := Until("k")
	if got.Before(before.Add(59 * time.Minute)) {
		t.Errorf("deadline too early: %v", got)
	}
}

func TestLoad_SkipsExpired(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	Path = dir + "/snooze.json"

	// Write a mixed file: one fresh, one expired.
	Set("fresh", time.Hour)
	Set("expired", time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	reset(t)
	Load()

	if Active("expired") {
		t.Error("expired entry should not load")
	}
	if !Active("fresh") {
		t.Error("fresh entry should load")
	}
}
