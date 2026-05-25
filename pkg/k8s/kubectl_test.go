package k8s

import (
	"strings"
	"testing"
)

// TestAvailable_NoCrash ensures Available() never panics regardless of
// whether kubectl is installed in the test environment.
func TestAvailable_NoCrash(t *testing.T) {
	_ = Available()
}

// TestListPods_NoKubectl verifies that ListPods returns a clear error message
// when kubectl is not on PATH, rather than panicking or hanging.
func TestListPods_NoKubectl(t *testing.T) {
	if Available() {
		t.Skip("kubectl is installed — skipping no-kubectl path")
	}
	_, err := ListPods("")
	if err == nil {
		t.Fatal("expected error when kubectl is not available")
	}
	if !strings.Contains(err.Error(), "kubectl not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestRestartDeployment_NoKubectl checks the same for RestartDeployment.
func TestRestartDeployment_NoKubectl(t *testing.T) {
	if Available() {
		t.Skip("kubectl is installed — skipping no-kubectl path")
	}
	_, err := RestartDeployment("default", "test-deploy")
	if err == nil {
		t.Fatal("expected error when kubectl is not available")
	}
	if !strings.Contains(err.Error(), "kubectl not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}
