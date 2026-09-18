package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Skill represents a procedural SRE skill parsed from a Markdown file with frontmatter
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	FilePath    string `json:"file_path"`
}

// Manager handles loading, viewing, creating, and updating skills in a directory
type Manager struct {
	mu        sync.RWMutex
	skillsDir string
	skills    map[string]Skill
}

// NewManager initializes the skills directory and loads existing skills
func NewManager(dir string) (*Manager, error) {
	if dir == "" {
		dir = "skills"
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create skills directory: %w", err)
	}

	mgr := &Manager{
		skillsDir: dir,
		skills:    make(map[string]Skill),
	}

	if err := mgr.Reload(); err != nil {
		return nil, err
	}

	return mgr, nil
}

// Reload scans the skills directory and reloads all .md files
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.skills = make(map[string]Skill)

	entries, err := os.ReadDir(m.skillsDir)
	if err != nil {
		return fmt.Errorf("failed to read skills directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		fullPath := filepath.Join(m.skillsDir, entry.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		skill, err := parseSkillFile(entry.Name(), string(data), fullPath)
		if err == nil {
			m.skills[skill.Name] = skill
		}
	}

	return nil
}

// parseSkillFile extracts frontmatter (name & description) and the markdown body
func parseSkillFile(fileName, content, fullPath string) (Skill, error) {
	defaultName := strings.TrimSuffix(fileName, ".md")
	skill := Skill{
		Name:     defaultName,
		FilePath: fullPath,
		Content:  content,
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		inFrontmatter := true
		var bodyLines []string
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			trimmed := strings.TrimSpace(line)
			if inFrontmatter && trimmed == "---" {
				inFrontmatter = false
				continue
			}

			if inFrontmatter {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					key := strings.ToLower(strings.TrimSpace(parts[0]))
					val := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
					switch key {
					case "name":
						if val != "" {
							skill.Name = val
						}
					case "description":
						skill.Description = val
					}
				}
			} else {
				bodyLines = append(bodyLines, line)
			}
		}
		skill.Content = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	}

	if skill.Description == "" {
		skill.Description = fmt.Sprintf("Procedural runbook for %s", skill.Name)
	}

	return skill, nil
}

// GetSummaryPrompt returns a formatted catalog string to be injected into LLM System Prompt
func (m *Manager) GetSummaryPrompt() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.skills) == 0 {
		return "Tiada custom SRE skills tersedia buat masa ini."
	}

	var sb strings.Builder
	sb.WriteString("Available SRE Procedural Skills (Load on-demand with `skill_view`):\n")
	for _, s := range m.skills {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", s.Name, s.Description))
	}
	return sb.String()
}

// ViewSkill retrieves the full content of a specific skill
func (m *Manager) ViewSkill(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.skills[name]
	if !ok {
		return "", fmt.Errorf("skill '%s' tidak dijumpai", name)
	}

	return fmt.Sprintf("# Skill: %s\n**Description:** %s\n\n%s", s.Name, s.Description, s.Content), nil
}

// SaveSkill creates or updates a skill file on disk and updates the in-memory cache
func (m *Manager) SaveSkill(name, description, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("skill name cannot be empty")
	}

	cleanName := strings.ReplaceAll(name, " ", "-")
	fileName := fmt.Sprintf("%s.md", cleanName)
	filePath := filepath.Join(m.skillsDir, fileName)

	if description == "" {
		description = fmt.Sprintf("SRE runbook for %s", cleanName)
	}

	fileContent := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", cleanName, description, strings.TrimSpace(body))

	if err := os.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
		return fmt.Errorf("failed to write skill file: %w", err)
	}

	m.skills[cleanName] = Skill{
		Name:        cleanName,
		Description: description,
		Content:     strings.TrimSpace(body),
		FilePath:    filePath,
	}

	return nil
}

// DeleteSkill removes a skill file
func (m *Manager) DeleteSkill(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.skills[name]
	if !ok {
		return fmt.Errorf("skill '%s' does not exist", name)
	}

	if err := os.Remove(s.FilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete skill file: %w", err)
	}

	delete(m.skills, name)
	return nil
}
