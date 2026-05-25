package actions

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

const terraformTimeout = 5 * time.Minute

// TerraformApply runs a terraform apply command in the configured directory.
// The working directory is taken from TERRAFORM_DIR (default "./terraform"),
// and the run is bounded by a 5-minute context timeout so a stuck plan
// cannot hang the bot indefinitely.
//
// Safety note: this uses -auto-approve. Gate the calling button (or the AI
// destructive-tool safe mode) before exposing this to untrusted operators.
func TerraformApply() (string, error) {
	tfDir := os.Getenv("TERRAFORM_DIR")
	if tfDir == "" {
		tfDir = "./terraform"
	}

	if info, err := os.Stat(tfDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("terraform dir %q not accessible: %w", tfDir, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), terraformTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "terraform", "apply", "-auto-approve")
	cmd.Dir = tfDir

	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("terraform apply timed out after %s", terraformTimeout)
	}
	if err != nil {
		return string(out), fmt.Errorf("terraform apply failed: %w", err)
	}

	return "Terraform apply completed successfully!\n" + string(out), nil
}
