package audit

import (
	"testing"
)

func setupAuditFile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()

	mu.Lock()
	path = dir + "/audit.log"
	chainHash = ""
	mu.Unlock()

	// Seed a handful of events spanning different types/actors.
	Log(Event{Type: "boot", Actor: "system", Status: "ok"})
	Log(Event{Type: "action", Actor: "user", Action: "RestartContainer", Target: "nginx", Status: "ok"})
	Log(Event{Type: "action", Actor: "user", Action: "StopContainer", Target: "redis-1", Status: "ok"})
	Log(Event{Type: "alert", Actor: "system", Action: "container_down", Target: "redis-1", Status: "info"})
	Log(Event{Type: "autopilot", Actor: "ai", Action: "RestartContainer", Target: "redis-1", Status: "ok", Detail: "attempt 1/3"})
}

func TestRecent_ReturnsNewestFirst(t *testing.T) {
	setupAuditFile(t)

	got, err := Recent(3)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	if got[0].Action != "RestartContainer" || got[0].Actor != "ai" {
		t.Errorf("expected newest (autopilot RestartContainer) first, got %+v", got[0])
	}
}

func TestSearch_SubstringMatch(t *testing.T) {
	setupAuditFile(t)

	got, err := Search("redis", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("want 3 redis-related entries, got %d", len(got))
	}
	for _, e := range got {
		if e.Target != "redis-1" {
			t.Errorf("unexpected target %q in redis search", e.Target)
		}
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	setupAuditFile(t)

	got, err := Search("RESTART", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) < 2 {
		t.Errorf("want >=2 restart matches, got %d", len(got))
	}
}

func TestByActor_FiltersExact(t *testing.T) {
	setupAuditFile(t)

	got, err := ByActor("ai", 10)
	if err != nil {
		t.Fatalf("ByActor: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("want 1 ai-actor entry, got %d", len(got))
	}
	if got[0].Type != "autopilot" {
		t.Errorf("expected autopilot entry, got %s", got[0].Type)
	}
}

func TestSearch_EmptyQueryReturnsRecent(t *testing.T) {
	setupAuditFile(t)

	got, err := Search("", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 entries, got %d", len(got))
	}
}

func TestRecent_MissingFile(t *testing.T) {
	mu.Lock()
	path = t.TempDir() + "/nonexistent.log"
	chainHash = ""
	mu.Unlock()

	_, err := Recent(5)
	if err != ErrLogMissing {
		t.Errorf("want ErrLogMissing, got %v", err)
	}
}
