package actions

import (
	"fmt"
	"os/exec"
)

// TerraformApply runs a terraform apply command in the terraform directory.
// For safety in this demo, it uses -auto-approve.
func TerraformApply() (string, error) {
	// Path to terraform folder relative to project root
	tfDir := "./terraform"

	cmd := exec.Command("terraform", "apply", "-auto-approve")
	cmd.Dir = tfDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("terraform apply failed: %v", err)
	}

	return "Terraform apply completed successfully!\n" + string(out), nil
}

// ScaleRedis updates the variables.tf or a tfvars file to scale nodes.
// For simplicity in this agentic demo, we might just trigger a re-apply 
// if the user has manually changed the file, or we could use sed/regex to update it.
func ScaleRedis(count int) (string, error) {
	// This is a placeholder for a more complex file editing logic.
	// For now, let's just return a message saying it's triggered.
	return fmt.Sprintf("Scaling Redis to %d nodes... (Logic to update .tf files pending implementation)", count), nil
}
