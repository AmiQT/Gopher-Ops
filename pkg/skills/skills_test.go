package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gopher-skills-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to init manager: %v", err)
	}

	// 1. Create a skill
	skillName := "docker-oom-triage"
	desc := "Triage OOM killed containers and restart safely"
	body := "### Steps:\n1. Check logs\n2. Inspect memory limit\n3. Restart container"

	err = mgr.SaveSkill(skillName, desc, body)
	if err != nil {
		t.Fatalf("failed to save skill: %v", err)
	}

	// 2. View Skill
	viewContent, err := mgr.ViewSkill(skillName)
	if err != nil {
		t.Fatalf("failed to view skill: %v", err)
	}
	if !strings.Contains(viewContent, "Triage OOM killed") || !strings.Contains(viewContent, "Check logs") {
		t.Errorf("viewContent missing expected details: %s", viewContent)
	}

	// 3. Prompt Summary
	summary := mgr.GetSummaryPrompt()
	if !strings.Contains(summary, "docker-oom-triage") {
		t.Errorf("summary prompt missing skill name: %s", summary)
	}

	// 4. Reload from disk
	mgr2, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to reload manager: %v", err)
	}
	if _, err := mgr2.ViewSkill(skillName); err != nil {
		t.Errorf("reloaded manager failed to find skill: %v", err)
	}

	// 5. Delete Skill
	if err := mgr.DeleteSkill(skillName); err != nil {
		t.Fatalf("failed to delete skill: %v", err)
	}
	if _, err := mgr.ViewSkill(skillName); err == nil {
		t.Errorf("expected skill to be deleted, but found it")
	}

	// Verify file is gone
	filePath := filepath.Join(tempDir, skillName+".md")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("expected skill file to be removed from disk")
	}
}
