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

	return intents
}
