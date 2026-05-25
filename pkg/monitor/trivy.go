package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"gopher-ops/pkg/docker"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

// TrivyAvailable returns true when the trivy binary is on PATH.
func TrivyAvailable() bool {
	_, err := exec.LookPath("trivy")
	return err == nil
}

// trivyVuln is the subset of a Trivy JSON result we care about.
type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
}

type trivyResult struct {
	Target          string      `json:"Target"`
	Vulnerabilities []trivyVuln `json:"Vulnerabilities"`
}

type trivyReport struct {
	Results []trivyResult `json:"Results"`
}

// scanImageWithTrivy runs `trivy image --format json --quiet <image>` and
// returns a human-readable summary.  A 5-minute timeout guards against Trivy
// pulling a fresh DB on first run.
func scanImageWithTrivy(image string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "trivy", "image",
		"--format", "json",
		"--quiet",
		"--no-progress",
		image,
	)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("trivy timed out scanning %s", image)
	}
	if err != nil {
		return "", fmt.Errorf("trivy error for %s: %w", image, err)
	}

	var report trivyReport
	if err := json.Unmarshal(out, &report); err != nil {
		return "", fmt.Errorf("trivy parse error: %w", err)
	}

	counts := map[string]int{"CRITICAL": 0, "HIGH": 0, "MEDIUM": 0, "LOW": 0, "UNKNOWN": 0}
	var critHigh []trivyVuln
	for _, result := range report.Results {
		for _, v := range result.Vulnerabilities {
			counts[v.Severity]++
			if v.Severity == "CRITICAL" || v.Severity == "HIGH" {
				critHigh = append(critHigh, v)
			}
		}
	}

	total := counts["CRITICAL"] + counts["HIGH"] + counts["MEDIUM"] + counts["LOW"] + counts["UNKNOWN"]
	if total == 0 {
		return fmt.Sprintf("✅ %s — no vulnerabilities found by Trivy.", image), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🔍 **%s** — %d vulns (C:%d H:%d M:%d L:%d)\n",
		image, total, counts["CRITICAL"], counts["HIGH"], counts["MEDIUM"], counts["LOW"]))

	// Show top 5 CRITICAL/HIGH entries so the AI prompt stays bounded.
	shown := 0
	for _, v := range critHigh {
		if shown >= 5 {
			break
		}
		fix := v.FixedVersion
		if fix == "" {
			fix = "no fix yet"
		}
		sb.WriteString(fmt.Sprintf("  - **%s** [%s] %s@%s → fix: %s\n",
			v.Severity, v.VulnerabilityID, v.PkgName, v.InstalledVersion, fix))
		shown++
	}
	if len(critHigh) > 5 {
		sb.WriteString(fmt.Sprintf("  _...and %d more CRITICAL/HIGH_\n", len(critHigh)-5))
	}
	return sb.String(), nil
}

// GetImageSecurityReport scans all container images.
// If Trivy is on PATH it runs a real CVE scan per image; otherwise it falls
// back to the lightweight pattern-match list so the function always returns
// something useful regardless of the environment.
func GetImageSecurityReport() (string, error) {
	cli, err := docker.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get docker client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("failed to list containers: %w", err)
	}

	if !TrivyAvailable() {
		return getImageSecurityReportFallback(containers), nil
	}

	// De-dup images across containers.
	seen := make(map[string]bool)
	var sb strings.Builder
	sb.WriteString("🛡️ **VULNERABILITY SCAN REPORT (Trivy)**\n\n")

	for _, c := range containers {
		if seen[c.Image] {
			continue
		}
		seen[c.Image] = true
		result, err := scanImageWithTrivy(c.Image)
		if err != nil {
			sb.WriteString(fmt.Sprintf("⚠️ %s — scan error: %v\n", c.Image, err))
			continue
		}
		sb.WriteString(result + "\n")
	}
	return sb.String(), nil
}

// getImageSecurityReportFallback is the original hardcoded-pattern check,
// kept as a fallback when Trivy is not installed.
func getImageSecurityReportFallback(containers []dockertypes.Container) string {
	knownVulnerable := map[string]string{
		"nginx:1.18":  "CRITICAL: CVE-2021-23017 (Off-by-one in resolver). Upgrade to 1.21+",
		"redis:5.0":   "HIGH: CVE-2022-24736 (Lua script vulnerability). Upgrade to 6.0+",
		"postgres:10": "MEDIUM: Out of support. Upgrade to 13+",
	}

	var sb strings.Builder
	sb.WriteString("VULNERABILITY SCAN REPORT (BASIC PATTERN CHECK):\n")
	sb.WriteString("⚠️  Trivy not found — showing limited hardcoded pattern match only.\n")
	sb.WriteString("    Install Trivy for real CVE coverage: https://aquasecurity.github.io/trivy\n\n")

	foundCount := 0
	for _, c := range containers {
		for vulnImg, reason := range knownVulnerable {
			if strings.Contains(c.Image, vulnImg) {
				foundCount++
				name := strings.TrimPrefix(c.Names[0], "/")
				sb.WriteString(fmt.Sprintf("- ⚠️ **%s** (%s)\n  Status: %s\n", name, c.Image, reason))
			}
		}
	}
	if foundCount == 0 {
		sb.WriteString("No matches against the pattern list. (Real CVEs may still exist — install Trivy.)\n")
	}
	return sb.String()
}
