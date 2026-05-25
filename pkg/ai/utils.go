package ai

import (
	"regexp"
)

// ExtractIntents uses regex to find standard Gopher-Ops actions in LLM output
func ExtractIntents(output string) []ActionIntent {
	var intents []ActionIntent

	// STOP / SHUTDOWN CONTAINER
	stopRe := regexp.MustCompile(`(?:Stop|Shutdown)Container\(['"]?([a-zA-Z0-9]+)['"]?\)`)
	for _, matches := range stopRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "StopContainer", Target: matches[1]})
		}
	}

	// START CONTAINER
	startRe := regexp.MustCompile(`StartContainer\(['"]?([a-zA-Z0-9]+)['"]?\)`)
	for _, matches := range startRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "StartContainer", Target: matches[1]})
		}
	}

	// RESTART CONTAINER
	restartRe := regexp.MustCompile(`RestartContainer\(['"]?([a-zA-Z0-9]+)['"]?\)`)
	for _, matches := range restartRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "RestartContainer", Target: matches[1]})
		}
	}

	// CLEAR CACHE
	clearCacheRe := regexp.MustCompile(`ClearCache\(\)`)
	if clearCacheRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "ClearCache", Target: ""})
	}
	
	// VIEW LOGS
	logsRe := regexp.MustCompile(`ViewLogs\(['"]?([a-zA-Z0-9]+)['"]?\)`)
	for _, matches := range logsRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "ViewLogs", Target: matches[1]})
		}
	}

	// TERRAFORM APPLY
	tfApplyRe := regexp.MustCompile(`TerraformApply\(\)`)
	if tfApplyRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "TerraformApply", Target: ""})
	}

	// AUDIT SECURITY
	auditRe := regexp.MustCompile(`AuditSecurity\(\)`)
	if auditRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "AuditSecurity", Target: ""})
	}

	// VISUAL METRICS
	metricsRe := regexp.MustCompile(`VisualMetrics\(\)`)
	if metricsRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "VisualMetrics", Target: ""})
	}

	// CONTAINER STATS (per-container live CPU/RAM)
	containerStatsRe := regexp.MustCompile(`ContainerStats\(\)`)
	if containerStatsRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "ContainerStats", Target: ""})
	}

	// DISK USAGE (host filesystem usage)
	diskRe := regexp.MustCompile(`DiskUsage\(\)`)
	if diskRe.MatchString(output) {
		intents = append(intents, ActionIntent{Action: "DiskUsage", Target: ""})
	}

	// UPDATE RESOURCES — UpdateResources('<id>', cpuCores, memMB)
	updateRe := regexp.MustCompile(`UpdateResources\(['"]?([a-zA-Z0-9_-]+)['"]?`)
	for _, matches := range updateRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "UpdateResources", Target: matches[1]})
		}
	}

	// SEARCH LOGS — SearchLogs('<id>', 'pattern')
	searchRe := regexp.MustCompile(`SearchLogs\(['"]?([a-zA-Z0-9_-]+)['"]?`)
	for _, matches := range searchRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "SearchLogs", Target: matches[1]})
		}
	}

	// KUBERNETES — K8sListPods(), K8sDescribePod('<pod>'), K8sRestartDeployment('<deploy>')
	if regexp.MustCompile(`K8sListPods\(\)`).MatchString(output) {
		intents = append(intents, ActionIntent{Action: "K8sListPods", Target: ""})
	}
	k8sPodRe := regexp.MustCompile(`K8sDescribePod\(['"]?([a-zA-Z0-9_/-]+)['"]?`)
	for _, matches := range k8sPodRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "K8sDescribePod", Target: matches[1]})
		}
	}
	k8sDeployRe := regexp.MustCompile(`K8sRestartDeployment\(['"]?([a-zA-Z0-9_/-]+)['"]?`)
	for _, matches := range k8sDeployRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "K8sRestartDeployment", Target: matches[1]})
		}
	}

	// CHAOS — ChaosKill('<name>') or ChaosKillRandom()
	if regexp.MustCompile(`ChaosKillRandom\(\)`).MatchString(output) {
		intents = append(intents, ActionIntent{Action: "ChaosKillRandom", Target: ""})
	}
	chaosRe := regexp.MustCompile(`ChaosKill\(['"]?([a-zA-Z0-9_/-]+)['"]?`)
	for _, matches := range chaosRe.FindAllStringSubmatch(output, -1) {
		if len(matches) > 1 {
			intents = append(intents, ActionIntent{Action: "ChaosKill", Target: matches[1]})
		}
	}

	return intents
}
