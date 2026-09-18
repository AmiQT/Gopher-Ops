package ai

import (
	"github.com/google/generative-ai-go/genai"
)

// GetLocalTools returns the list of local actions and autonomous agent tools as genai.FunctionDeclaration
func GetLocalTools() []*genai.FunctionDeclaration {
	return []*genai.FunctionDeclaration{
		// --- Docker Container Actions ---
		{
			Name:        "RestartContainer",
			Description: "Restarts a specific Docker container by its ID.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID or name of the Docker container to restart.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "StopContainer",
			Description: "Stops a running Docker container.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID of the container to stop.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "GetContainerLogs",
			Description: "Fetches the last 10 lines of logs for a container.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The ID of the container.",
					},
				},
				Required: []string{"containerID"},
			},
		},

		// --- Deep RCA & Probe Tools ---
		{
			Name:        "docker_logs",
			Description: "Fetches container logs with optional line count and keyword filter for deep root-cause inspection.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The container ID or name to inspect.",
					},
					"lines": {
						Type:        genai.TypeNumber,
						Description: "Number of recent log lines to fetch (default: 50).",
					},
					"filter": {
						Type:        genai.TypeString,
						Description: "Optional case-insensitive substring to filter log lines (e.g. 'error', 'fatal', 'panic').",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "docker_inspect",
			Description: "Performs deep post-mortem inspection on a container (exit code, OOMKilled flag, memory/CPU limits, health probes, restart count).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The container ID or name to inspect.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "docker_network_inspect",
			Description: "Inspects container network attachments, IP addresses, port bindings, and DNS overrides to diagnose connectivity issues.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"containerID": {
						Type:        genai.TypeString,
						Description: "The container ID or name to inspect.",
					},
				},
				Required: []string{"containerID"},
			},
		},
		{
			Name:        "k8s_describe_pod",
			Description: "Inspects detailed Kubernetes pod status, events, CrashLoopBackOff reasons, and condition flags.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"podName": {
						Type:        genai.TypeString,
						Description: "The name of the Kubernetes pod.",
					},
					"namespace": {
						Type:        genai.TypeString,
						Description: "The Kubernetes namespace (default: 'default').",
					},
				},
				Required: []string{"podName"},
			},
		},
		{
			Name:        "k8s_pod_logs",
			Description: "Fetches timestamped logs for a Kubernetes pod to investigate runtime errors.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"podName": {
						Type:        genai.TypeString,
						Description: "The name of the Kubernetes pod.",
					},
					"namespace": {
						Type:        genai.TypeString,
						Description: "The Kubernetes namespace (default: 'default').",
					},
					"lines": {
						Type:        genai.TypeNumber,
						Description: "Number of log lines to tail (default: 50).",
					},
				},
				Required: []string{"podName"},
			},
		},

		// --- Procedural Skills Management (Hermes Engine Pattern) ---
		{
			Name:        "skill_view",
			Description: "Loads and views the full procedural markdown runbook for a specific SRE skill.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"name": {
						Type:        genai.TypeString,
						Description: "The name of the skill to view (e.g. docker-oom-triage, k8s-pod-restart).",
					},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "skill_manage",
			Description: "Creates, patches, or updates an SRE skill / runbook on disk so the agent can learn and self-improve.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type:        genai.TypeString,
						Description: "Action to perform: 'create', 'patch', or 'delete'.",
					},
					"name": {
						Type:        genai.TypeString,
						Description: "The unique name of the skill (e.g. redis-cluster-recovery).",
					},
					"description": {
						Type:        genai.TypeString,
						Description: "Short one-line description of what this skill does.",
					},
					"content": {
						Type:        genai.TypeString,
						Description: "The full Markdown runbook body with step-by-step instructions.",
					},
				},
				Required: []string{"action", "name"},
			},
		},

		// --- Persistent Memory Management (Hermes Engine Pattern) ---
		{
			Name:        "memory_view",
			Description: "Views all persistent SRE environment facts, architecture notes, and learned incident patterns.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{},
			},
		},
		{
			Name:        "memory_save",
			Description: "Saves a durable SRE fact, configuration detail, or post-incident lesson to long-term memory.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"fact": {
						Type:        genai.TypeString,
						Description: "A concise declarative fact to remember (e.g. 'Production DB pool size is 25 on 10.0.1.5').",
					},
				},
				Required: []string{"fact"},
			},
		},

		// --- Incident Post-Mortem & Reporting Tools ---
		{
			Name:        "generate_postmortem_report",
			Description: "Generates and saves a formal SRE post-mortem markdown report in the reports/ directory after an incident is resolved.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"title": {
						Type:        genai.TypeString,
						Description: "Title of the incident (e.g. 'Redis High Memory OOM Crash').",
					},
					"severity": {
						Type:        genai.TypeString,
						Description: "Severity level: CRITICAL, HIGH, MEDIUM, or LOW.",
					},
					"service": {
						Type:        genai.TypeString,
						Description: "Affected service or container name.",
					},
					"summary": {
						Type:        genai.TypeString,
						Description: "Executive summary of what happened and business impact.",
					},
					"root_cause": {
						Type:        genai.TypeString,
						Description: "Detailed root cause analysis explaining why the failure occurred.",
					},
					"log_evidence": {
						Type:        genai.TypeString,
						Description: "Relevant log snippets or error messages verifying the root cause.",
					},
					"action_taken": {
						Type:        genai.TypeString,
						Description: "Remediation actions taken to recover the service.",
					},
					"prevention_plan": {
						Type:        genai.TypeString,
						Description: "Actionable preventative measures, config updates, or alert rules added.",
					},
				},
				Required: []string{"title", "severity", "service", "summary", "root_cause", "action_taken"},
			},
		},
		{
			Name:        "list_incident_reports",
			Description: "Lists recent archived SRE incident post-mortem reports.",
			Parameters: &genai.Schema{
				Type:       genai.TypeObject,
				Properties: map[string]*genai.Schema{},
			},
		},
	}
}
