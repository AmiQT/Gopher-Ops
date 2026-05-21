package actions

import (
	"strings"
	"testing"
)

func TestClearCache(t *testing.T) {
	result := ClearCache()
	if !strings.Contains(result, "System cache cleared successfully") {
		t.Errorf("unexpected ClearCache result: %q", result)
	}
}

func TestInvestigateNetwork_ReturnsAnalysis(t *testing.T) {
	result := InvestigateNetwork("any-id")
	if result == "" {
		t.Error("expected non-empty network analysis")
	}
	if !strings.Contains(strings.ToUpper(result), "NETWORK") {
		t.Errorf("expected NETWORK in result, got: %q", result)
	}
}

func TestCheckConfig_ReturnsAnalysis(t *testing.T) {
	result := CheckConfig("any-id")
	if result == "" {
		t.Error("expected non-empty config analysis")
	}
	if !strings.Contains(strings.ToUpper(result), "CONFIG") {
		t.Errorf("expected CONFIG in result, got: %q", result)
	}
}

// GetContainerLogs, RestartContainer, StartContainer, StopContainer all require
// a live Docker daemon — covered by integration tests, not unit tests.

func TestScaleRedis_ReturnsMessage(t *testing.T) {
	result, err := ScaleRedis(3)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "3") {
		t.Errorf("expected node count '3' in result, got: %q", result)
	}
}

// TerraformPlan and TerraformApply require a real terraform binary and ./terraform dir —
// covered by integration tests, not unit tests.

