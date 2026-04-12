package actions

import (
	"strings"
	"testing"
)

func TestClearCache(t *testing.T) {
	result := ClearCache()
	expectedSubStr := "System cache cleared successfully"
	
	if !strings.Contains(result, expectedSubStr) {
		t.Errorf("Expected ClearCache to contain '%s', got: '%s'", expectedSubStr, result)
	}
}
