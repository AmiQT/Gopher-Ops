package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store manages persistent SRE facts across sessions
type Store struct {
	mu       sync.RWMutex
	dir      string
	filePath string
	facts    []string
}

// NewStore initializes the memory directory and loads existing facts
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		dir = "memories"
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create memories directory: %w", err)
	}

	s := &Store{
		dir:      dir,
		filePath: filepath.Join(dir, "SRE_FACTS.md"),
		facts:    make([]string, 0),
	}

	if err := s.Load(); err != nil {
		return nil, err
	}

	return s, nil
}

// Load reads facts from the markdown file
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.facts = make([]string, 0)

	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// Initialize empty file with header
		initialContent := "# Gopher-Ops SRE Persistent Memory\n// Durable environment facts, recurring incident patterns & architecture notes.\n\n"
		if err := os.WriteFile(s.filePath, []byte(initialContent), 0644); err != nil {
			return fmt.Errorf("failed to create initial memory file: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read memory file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") {
			fact := strings.TrimPrefix(trimmed, "- ")
			if fact != "" {
				s.facts = append(s.facts, fact)
			}
		}
	}

	return nil
}

// SaveFact appends a new durable fact with timestamp
func (s *Store) SaveFact(fact string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cleanFact := strings.TrimSpace(fact)
	if cleanFact == "" {
		return fmt.Errorf("fact cannot be empty")
	}

	targetFact := cleanFact
	if idx := strings.Index(cleanFact, "] "); idx != -1 && strings.HasPrefix(cleanFact, "[") {
		targetFact = cleanFact[idx+2:]
	}

	// Avoid duplicates
	for _, existing := range s.facts {
		existingClean := existing
		if idx := strings.Index(existing, "] "); idx != -1 && strings.HasPrefix(existing, "[") {
			existingClean = existing[idx+2:]
		}
		if strings.EqualFold(existingClean, targetFact) {
			return nil // Already saved
		}
	}

	timestamp := time.Now().Format("2006-01-02 15:04")
	entryWithDate := fmt.Sprintf("[%s] %s", timestamp, targetFact)
	s.facts = append(s.facts, entryWithDate)

	// Persist to file
	var sb strings.Builder
	sb.WriteString("# Gopher-Ops SRE Persistent Memory\n// Durable environment facts, recurring incident patterns & architecture notes.\n\n")
	for _, f := range s.facts {
		sb.WriteString(fmt.Sprintf("- %s\n", f))
	}

	if err := os.WriteFile(s.filePath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to persist memory: %w", err)
	}

	return nil
}

// GetPrompt formats the memory facts for inclusion in the AI System Prompt
func (s *Store) GetPrompt() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.facts) == 0 {
		return "Tiada persistent memory SRE direkodkan setakat ini."
	}

	var sb strings.Builder
	sb.WriteString("Persistent SRE Knowledge Base & Environment Facts:\n")
	for _, f := range s.facts {
		sb.WriteString(fmt.Sprintf("- %s\n", f))
	}
	return sb.String()
}

// ListFacts returns all stored facts as a slice of strings
func (s *Store) ListFacts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]string, len(s.facts))
	copy(res, s.facts)
	return res
}

// Clear wipes all recorded facts
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.facts = make([]string, 0)
	initialContent := "# Gopher-Ops SRE Persistent Memory\n// Durable environment facts, recurring incident patterns & architecture notes.\n\n"
	return os.WriteFile(s.filePath, []byte(initialContent), 0644)
}
