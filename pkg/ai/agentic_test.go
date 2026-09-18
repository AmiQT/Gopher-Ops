package ai

import (
	"os"
	"strings"
	"testing"

	"gopher-ops/pkg/memory"
	"gopher-ops/pkg/skills"
)

func TestBuildSystemPrompt_WithSkillsAndMemory(t *testing.T) {
	tempSkills, err := os.MkdirTemp("", "gopher-test-skills-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempSkills)

	tempMem, err := os.MkdirTemp("", "gopher-test-mem-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempMem)

	skillsMgr, err := skills.NewManager(tempSkills)
	if err != nil {
		t.Fatalf("failed to init skills: %v", err)
	}
	_ = skillsMgr.SaveSkill("test-sre-skill", "A test skill", "Step 1: Test")

	memStore, err := memory.NewStore(tempMem)
	if err != nil {
		t.Fatalf("failed to init memory: %v", err)
	}
	_ = memStore.SaveFact("PostgreSQL port is 5432")

	prompt := buildSystemPrompt(skillsMgr, memStore)

	if !strings.Contains(prompt, "test-sre-skill") {
		t.Errorf("expected prompt to contain skill name: %s", prompt)
	}
	if !strings.Contains(prompt, "PostgreSQL port is 5432") {
		t.Errorf("expected prompt to contain memory fact: %s", prompt)
	}
	if !strings.Contains(prompt, "Autonomous Senior SRE & AI Automation Engineer") {
		t.Errorf("expected prompt to contain base prompt: %s", prompt)
	}
}

func TestGetLocalTools_ContainsAgenticTools(t *testing.T) {
	tools := GetLocalTools()

	expectedTools := map[string]bool{
		"RestartContainer":       false,
		"StopContainer":          false,
		"GetContainerLogs":       false,
		"docker_logs":            false,
		"docker_inspect":         false,
		"docker_network_inspect": false,
		"k8s_describe_pod":       false,
		"k8s_pod_logs":           false,
		"skill_view":                 false,
		"skill_manage":               false,
		"memory_view":                false,
		"memory_save":                false,
		"generate_postmortem_report": false,
		"list_incident_reports":      false,
	}

	for _, tool := range tools {
		if _, ok := expectedTools[tool.Name]; ok {
			expectedTools[tool.Name] = true
		}
	}

	for toolName, found := range expectedTools {
		if !found {
			t.Errorf("missing expected tool declaration: %s", toolName)
		}
	}
}
