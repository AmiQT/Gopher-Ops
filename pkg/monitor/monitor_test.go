package monitor

import (
	"testing"
)

func TestGetContainerName_Invalid(t *testing.T) {
	// For an invalid container ID, GetContainerName should just return the ID itself.
	invalidID := "abc123invalid_docker_id"
	name := GetContainerName(invalidID)
	
	if name != invalidID {
		t.Errorf("Expected GetContainerName to return the invalid ID '%s', got '%s'", invalidID, name)
	}
}
