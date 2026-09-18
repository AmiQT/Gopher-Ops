package memory

import (
	"os"
	"strings"
	"testing"
)

func TestMemoryStore(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gopher-memory-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	// 1. Save Fact
	fact := "Production Redis cluster runs on port 6379 with maxmemory 2GB"
	if err := store.SaveFact(fact); err != nil {
		t.Fatalf("failed to save fact: %v", err)
	}

	// 2. Duplicate Fact should be ignored
	if err := store.SaveFact(fact); err != nil {
		t.Fatalf("failed to save duplicate fact: %v", err)
	}

	// 3. Prompt check
	prompt := store.GetPrompt()
	if !strings.Contains(prompt, "Production Redis cluster") {
		t.Errorf("prompt missing saved fact: %s", prompt)
	}

	// 4. Reload check
	store2, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}
	facts := store2.ListFacts()
	if len(facts) != 1 || !strings.Contains(facts[0], "Production Redis cluster") {
		t.Errorf("unexpected facts after reload: %v", facts)
	}

	// 5. Clear check
	if err := store.Clear(); err != nil {
		t.Fatalf("failed to clear store: %v", err)
	}
	if len(store.ListFacts()) != 0 {
		t.Errorf("expected 0 facts after clear")
	}
}
