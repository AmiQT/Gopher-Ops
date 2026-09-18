package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/k8s"
	"gopher-ops/pkg/mcp"
	"gopher-ops/pkg/memory"
	"gopher-ops/pkg/monitor"
	"gopher-ops/pkg/report"
	"gopher-ops/pkg/skills"
)

func isDestructive(toolName string) bool {
	switch toolName {
	case "StopContainer", "RestartContainer":
		return true
	}
	return false
}

// ActionIntent structured data for hitting the HITL (Human-in-the-loop)
type ActionIntent struct {
	Action string
	Target string
}

type GeminiAgent struct {
	client      *genai.Client
	model       *genai.GenerativeModel
	session     *genai.ChatSession
	mcpManager  *mcp.MCPManager
	skillsMgr   *skills.Manager
	memoryStore *memory.Store
	reportMgr   *report.Manager
}

const baseSystemPrompt = `You are an Autonomous Senior SRE & AI Automation Engineer named "Gopher-Ops".
Your job is to monitor system health, diagnose infrastructure anomalies, and autonomously resolve issues using the provided tools.
You have access to REAL-TIME tools for Kubernetes (via MCP), Local Docker containers, Procedural SRE Skills, and Persistent Memory.

CRITICAL TONE REQUIREMENT:
Anda mesti membalas menggunakan Bahasa Melayu yang santai dan sopan (friendly but professional). 
Gunakan nada seorang jurutera yang berpengalaman tapi mesra. Elakkan penggunaan slang Gen Z yang berlebihan.
Sentiasa hormati Operator.

AUTONOMOUS CAPABILITIES (Hermes Engine Pattern):
1. **Procedural Skills ('skill_view', 'skill_manage')**:
   - Check the available skills summary below. If relevant to the incident (e.g. OOM triage, pod crash, deep RCA protocol), load the full runbook with 'skill_view'.
   - After resolving an unfamiliar incident, write or patch a runbook using 'skill_manage' so you remember the steps for future incidents!
2. **Persistent Memory ('memory_view', 'memory_save')**:
   - Save critical environment facts, IP addresses, cluster limits, and learned patterns with 'memory_save'.
3. **Evidence-First Deep RCA ('docker_inspect', 'docker_logs', 'docker_network_inspect', 'k8s_describe_pod', 'k8s_pod_logs')**:
   - Never guess or hallucinate root causes. Inspect raw post-mortem data, filter logs for errors, check network binding, or describe pods to isolate smoking guns before suggesting fixes.

VERY IMPORTANT:
- Be EXTREMELY concise.
- ONLY answer what the user asks. 
- FORMATTING: Every time you list containers or pods, you MUST use a clean Bullet List with **Bold Headers**. Do NOT use Markdown Tables.

When diagnosing, follow this flow:
1. Examine the provided context, memory facts, and skill catalog silently.
2. Load a relevant skill with 'skill_view' if a procedure exists.
3. Call tools to gather data or execute safe remediation.
4. If an action is destructive, report it so the Operator can approve.
5. Explain the outcome and save any new lessons learned.`

// NewGeminiAgent sets up the generative model with MCP, Skills, and Memory integration
func NewGeminiAgent(apiKey, modelType string) (*GeminiAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}

	if modelType == "" {
		modelType = "gemini-2.5-flash"
	}
	model := client.GenerativeModel(modelType)
	model.SetTemperature(0.7)

	// 1. Initialize Skills Manager & Memory Store
	skillsMgr, err := skills.NewManager("skills")
	if err != nil {
		slog.Warn("Skills manager initialization issue", "error", err)
	}

	memoryStore, err := memory.NewStore("memories")
	if err != nil {
		slog.Warn("Memory store initialization issue", "error", err)
	}

	reportMgr, err := report.NewManager("reports")
	if err != nil {
		slog.Warn("Report manager initialization issue", "error", err)
	}

	// 2. Build Enriched System Prompt
	fullSystemPrompt := buildSystemPrompt(skillsMgr, memoryStore)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(fullSystemPrompt),
		},
	}

	// 3. Initialize MCP Manager
	mcpMgr, err := mcp.NewMCPManager()
	if err != nil {
		slog.Warn("MCP server unavailable — running in local-only mode", "error", err)
	}

	// 4. Register Tools
	var toolList []*genai.FunctionDeclaration

	// Add Local + Agentic Tools
	toolList = append(toolList, GetLocalTools()...)

	// Add MCP Tools (if available)
	if mcpMgr != nil {
		mcpTools, err := mcpMgr.ListTools(ctx)
		if err == nil {
			for _, t := range mcpTools {
				toolList = append(toolList, &genai.FunctionDeclaration{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  ConvertMCPSchemaToGenai(t.InputSchema),
				})
			}
		}
	}

	model.Tools = []*genai.Tool{
		{FunctionDeclarations: toolList},
	}

	session := model.StartChat()

	return &GeminiAgent{
		client:      client,
		model:       model,
		session:     session,
		mcpManager:  mcpMgr,
		skillsMgr:   skillsMgr,
		memoryStore: memoryStore,
		reportMgr:   reportMgr,
	}, nil
}

func buildSystemPrompt(skillsMgr *skills.Manager, memoryStore *memory.Store) string {
	var sb strings.Builder
	sb.WriteString(baseSystemPrompt)
	sb.WriteString("\n\n---\n")

	if skillsMgr != nil {
		sb.WriteString("### PROCEDURAL SKILLS CATALOG\n")
		sb.WriteString(skillsMgr.GetSummaryPrompt())
		sb.WriteString("\n\n")
	}

	if memoryStore != nil {
		sb.WriteString("### PERSISTENT SRE MEMORY\n")
		sb.WriteString(memoryStore.GetPrompt())
		sb.WriteString("\n")
	}

	return sb.String()
}

// ConvertMCPSchemaToGenai is a helper to bridge MCP JSON Schema to Gemini Schema
func ConvertMCPSchemaToGenai(mcpSchema interface{}) *genai.Schema {
	var schemaMap map[string]interface{}
	data, _ := json.Marshal(mcpSchema)
	json.Unmarshal(data, &schemaMap)

	schema := &genai.Schema{
		Type:       genai.TypeObject,
		Properties: make(map[string]*genai.Schema),
	}

	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		for name, p := range props {
			pMap := p.(map[string]interface{})
			propSchema := &genai.Schema{}

			switch pMap["type"].(string) {
			case "string":
				propSchema.Type = genai.TypeString
			case "number", "integer":
				propSchema.Type = genai.TypeNumber
			case "boolean":
				propSchema.Type = genai.TypeBoolean
			case "object":
				propSchema.Type = genai.TypeObject
			}

			if desc, ok := pMap["description"].(string); ok {
				propSchema.Description = desc
			}
			schema.Properties[name] = propSchema
		}
	}

	if req, ok := schemaMap["required"].([]interface{}); ok {
		for _, r := range req {
			schema.Required = append(schema.Required, r.(string))
		}
	}

	return schema
}

// ProcessRequest handles the user query and autonomous multi-turn tool execution
func (a *GeminiAgent) ProcessRequest(userMsg string) (string, []ActionIntent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	var intents []ActionIntent

	// 1. Gather current context
	health, _ := monitor.GetSystemHealth()
	enrichedPrompt := fmt.Sprintf("```\n%s\n```\nOperator Query: %s", health, userMsg)

	// 2. Send Message to AI with retry
	var resp *genai.GenerateContentResponse
	err := withRetry(ctx, func() error {
		r, e := a.session.SendMessage(ctx, genai.Text(enrichedPrompt))
		resp = r
		return e
	})
	if err != nil {
		return "", intents, err
	}

	// 3. Handle Tool Calls (The Autonomous ReAct Loop)
	maxTurns := 8
	for turn := 0; turn < maxTurns; turn++ {
		var toolCalls []genai.FunctionCall
		for _, cand := range resp.Candidates {
			for _, part := range cand.Content.Parts {
				if call, ok := part.(genai.FunctionCall); ok {
					toolCalls = append(toolCalls, call)
				}
			}
		}

		if len(toolCalls) == 0 {
			break // No more tools to call, return the final text
		}

		// Execute Tool Calls
		var toolResults []genai.Part
		for _, call := range toolCalls {
			slog.Info("🛠️ AI calling tool", "name", call.Name, "args", call.Args)

			result, execErr := a.dispatchTool(ctx, call.Name, call.Args)
			if execErr != nil {
				slog.Warn("Tool execution failed", "name", call.Name, "error", execErr)
				result = fmt.Sprintf("Tool %s failed: %v", call.Name, execErr)
			}

			toolResults = append(toolResults, genai.FunctionResponse{
				Name:     call.Name,
				Response: map[string]any{"result": result},
			})
		}

		// Send results back to AI to get final response or next tool call
		err = withRetry(ctx, func() error {
			r, e := a.session.SendMessage(ctx, toolResults...)
			resp = r
			return e
		})
		if err != nil {
			return "", intents, err
		}
	}

	// 4. Final Text Output
	var output string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if text, ok := part.(genai.Text); ok {
				output += string(text)
			}
		}
	}

	// 5. Extract HITL intents from text so the UI can render confirmation buttons
	intents = ExtractIntents(output)

	return output, intents, nil
}

// dispatchTool routes a Gemini function call to the correct backend:
// - Procedural Skills (skill_view, skill_manage)
// - Persistent Memory (memory_view, memory_save)
// - Local Docker operations
// - MCP manager (Kubernetes tools)
func (a *GeminiAgent) dispatchTool(ctx context.Context, name string, args map[string]any) (string, error) {
	containerID, _ := args["containerID"].(string)

	if isDestructive(name) && !destructiveAllowed() {
		audit.Log(audit.Event{
			Type: "ai_tool", Actor: "ai", Action: name, Target: containerID,
			Status: "blocked", Detail: "destructive AI tool blocked — HITL required",
		})
		return "", fmt.Errorf("destructive tool %s is disabled (set AI_DESTRUCTIVE_ALLOWED=true to enable, or suggest the action in text for a confirmation button)", name)
	}

	result, err := func() (string, error) {
		switch name {
		// --- Skills Tools ---
		case "skill_view":
			skillName, _ := args["name"].(string)
			if a.skillsMgr == nil {
				return "", fmt.Errorf("skills manager not initialized")
			}
			return a.skillsMgr.ViewSkill(skillName)

		case "skill_manage":
			action, _ := args["action"].(string)
			skillName, _ := args["name"].(string)
			desc, _ := args["description"].(string)
			content, _ := args["content"].(string)
			if a.skillsMgr == nil {
				return "", fmt.Errorf("skills manager not initialized")
			}
			switch strings.ToLower(action) {
			case "create", "patch", "update":
				err := a.skillsMgr.SaveSkill(skillName, desc, content)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Successfully saved skill '%s' to disk.", skillName), nil
			case "delete":
				err := a.skillsMgr.DeleteSkill(skillName)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Successfully deleted skill '%s'.", skillName), nil
			default:
				return "", fmt.Errorf("unknown skill action: %s", action)
			}

		// --- Memory Tools ---
		case "memory_view":
			if a.memoryStore == nil {
				return "", fmt.Errorf("memory store not initialized")
			}
			facts := a.memoryStore.ListFacts()
			if len(facts) == 0 {
				return "No persistent memory facts stored yet.", nil
			}
			return fmt.Sprintf("Stored SRE Facts (%d):\n- %s", len(facts), strings.Join(facts, "\n- ")), nil

		case "memory_save":
			fact, _ := args["fact"].(string)
			if a.memoryStore == nil {
				return "", fmt.Errorf("memory store not initialized")
			}
			if err := a.memoryStore.SaveFact(fact); err != nil {
				return "", err
			}
			return fmt.Sprintf("Successfully saved durable fact to persistent memory: %s", fact), nil

		// --- Incident Post-Mortem & Reporting Tools ---
		case "generate_postmortem_report":
			if a.reportMgr == nil {
				return "", fmt.Errorf("report manager not initialized")
			}
			title, _ := args["title"].(string)
			severity, _ := args["severity"].(string)
			service, _ := args["service"].(string)
			summary, _ := args["summary"].(string)
			rootCause, _ := args["root_cause"].(string)
			logEvidence, _ := args["log_evidence"].(string)
			actionTaken, _ := args["action_taken"].(string)
			preventionPlan, _ := args["prevention_plan"].(string)

			rep := report.IncidentReport{
				Title:          title,
				Severity:       severity,
				Service:        service,
				Summary:        summary,
				RootCause:      rootCause,
				LogEvidence:    logEvidence,
				ActionTaken:    actionTaken,
				PreventionPlan: preventionPlan,
			}
			path, err := a.reportMgr.Generate(rep)
			if err != nil {
				return "", fmt.Errorf("failed to generate post-mortem report: %w", err)
			}
			return fmt.Sprintf("Successfully generated and saved SRE incident post-mortem report: %s", path), nil

		case "list_incident_reports":
			if a.reportMgr == nil {
				return "", fmt.Errorf("report manager not initialized")
			}
			return a.reportMgr.ListRecent(10)

		// --- Docker Container Actions ---
		case "RestartContainer":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			return actions.RestartContainer(containerID)
		case "StopContainer":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			return actions.StopContainer(containerID)
		case "GetContainerLogs":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			return actions.GetContainerLogs(containerID)

		// --- Deep RCA & Probe Tools ---
		case "docker_logs":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			lines := 50
			if l, ok := args["lines"].(float64); ok && l > 0 {
				lines = int(l)
			}
			filter, _ := args["filter"].(string)
			if filter != "" {
				return actions.SearchLogs(containerID, filter, lines, 20)
			}
			return actions.GetContainerLogsN(containerID, lines)

		case "docker_inspect":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			crashCtx, err := monitor.GetCrashContext(containerID)
			if err != nil {
				return "", fmt.Errorf("inspect failed: %w", err)
			}
			return crashCtx.FormatForAI(), nil

		case "docker_network_inspect":
			if containerID == "" {
				return "", fmt.Errorf("missing containerID")
			}
			return actions.InvestigateNetwork(containerID), nil

		case "k8s_describe_pod":
			podName, _ := args["podName"].(string)
			namespace, _ := args["namespace"].(string)
			if podName == "" {
				return "", fmt.Errorf("missing podName")
			}
			return k8s.DescribePod(namespace, podName)

		case "k8s_pod_logs":
			podName, _ := args["podName"].(string)
			namespace, _ := args["namespace"].(string)
			lines := 50
			if l, ok := args["lines"].(float64); ok && l > 0 {
				lines = int(l)
			}
			if podName == "" {
				return "", fmt.Errorf("missing podName")
			}
			return k8s.GetPodLogs(namespace, podName, lines)

		default:
			if a.mcpManager != nil {
				return a.mcpManager.CallTool(ctx, name, args)
			}
			return "", fmt.Errorf("unknown tool: %s", name)
		}
	}()

	status := "ok"
	detail := ""
	if err != nil {
		status = "error"
		detail = err.Error()
	}
	audit.Log(audit.Event{
		Type: "ai_tool", Actor: "ai", Action: name, Target: containerID,
		Status: status, Detail: detail,
	})

	return result, err
}

// ResetSession clears the conversation history to save tokens
func (a *GeminiAgent) ResetSession() {
	a.session = a.model.StartChat()
}

// DiagnoseIssue performs root-cause analysis for a container failure.
func (a *GeminiAgent) DiagnoseIssue(containerName, evidence string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()

	prompt := fmt.Sprintf(`SYSTEM ALERT: Container %q failed. Perform a DEEP root-cause analysis.

You will receive three blocks of evidence:
1. CONTAINER POST-MORTEM — inspect data (exit code, OOMKilled, restart count, health probes).
2. SYSTEM METRICS — recent host CPU/RAM history.
3. CONTAINER LOGS — the most recent log tail.

Reasoning steps you MUST follow:
- First, distinguish the failure CLASS: OOM kill, panic/segfault, healthcheck failure, dependency failure, configuration error, or clean exit.
- Use the exit code, OOMKilled flag, and signal hints — do NOT trust logs alone.
- Correlate timing in the logs with the system metric points provided.
- Distinguish ROOT CAUSE from downstream SYMPTOMS (e.g. "connection refused" is usually a symptom, not the root).
- If evidence is insufficient, say so explicitly and list what you'd need.

%s

Respond in Bahasa Melayu using this structure:
- **Failure Class:** <one line>
- **Root Cause:** <2-3 sentences, specific>
- **Evidence:** <bullet list of the specific data points that support your conclusion>
- **Recommended Action:** <concrete fix, not generic advice>`, containerName, evidence)

	resp, err := a.send(ctx, prompt)
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// AuditSecurity provides a security assessment.
func (a *GeminiAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	prompt := fmt.Sprintf("SECURITY AUDIT REQUEST.\nCONTEXT:\n%s\nIMAGE DATA:\n%s\nAnalyze security in Bahasa Melayu.", ctxData, imageScanData)
	resp, err := a.send(ctx, prompt)
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// TriageIssue provides a deeper investigation flow.
func (a *GeminiAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	prompt := fmt.Sprintf("TRIAGE REQUEST: %s\nTarget: %s\nData:\n%s\nPerform triage in Bahasa Melayu.", triageType, containerName, data)
	resp, err := a.send(ctx, prompt)
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// send wraps session.SendMessage with retry/backoff
func (a *GeminiAgent) send(ctx context.Context, prompt string) (*genai.GenerateContentResponse, error) {
	var resp *genai.GenerateContentResponse
	err := withRetry(ctx, func() error {
		r, e := a.session.SendMessage(ctx, genai.Text(prompt))
		resp = r
		return e
	})
	return resp, err
}

// extractText gets text from genai.Response
func (a *GeminiAgent) extractText(resp *genai.GenerateContentResponse) string {
	var output string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			if text, ok := part.(genai.Text); ok {
				output += string(text)
			}
		}
	}
	return output
}

// Close cleans up
func (a *GeminiAgent) Close() {
	if a.client != nil {
		_ = a.client.Close()
	}
	if a.mcpManager != nil {
		a.mcpManager.Close()
	}
}

// IsOffline always returns false for GeminiAgent
func (a *GeminiAgent) IsOffline() bool { return false }
