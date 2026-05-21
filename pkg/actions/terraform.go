package actions

import (
	"fmt"
	"os/exec"
)

const tfDir = "./terraform"

// TerraformPlan runs terraform plan and returns the diff output for operator review.
// This must be shown to the operator before TerraformApply is called.
func TerraformPlan() (string, error) {
	cmd := exec.Command("terraform", "plan", "-no-color")
	cmd.Dir = tfDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("terraform plan failed: %v", err)
	}
	return string(out), nil
}

// TerraformApply runs terraform apply only after the operator has reviewed the plan.
// Never call this directly from AI-triggered paths — always go through TerraformPlan first.
func TerraformApply() (string, error) {
	cmd := exec.Command("terraform", "apply", "-auto-approve", "-no-color")
	cmd.Dir = tfDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("terraform apply failed: %v", err)
	}
	return "Terraform apply completed successfully!\n" + string(out), nil
}

// ScaleRedis updates the variables.tf or a tfvars file to scale nodes.
func ScaleRedis(count int) (string, error) {
	return fmt.Sprintf("Scaling Redis to %d nodes... (Logic to update .tf files pending implementation)", count), nil
}
