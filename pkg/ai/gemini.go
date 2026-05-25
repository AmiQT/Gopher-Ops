package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"

	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/mcp"
	"gopher-ops/pkg/monitor"
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
	client     *genai.Client
	model      *genai.GenerativeModel
	session    *genai.ChatSession
	mcpManager *mcp.MCPManager
}

const systemPrompt = `You are a Senior SRE & AI Automation Engineer named "Gopher-Ops".
Your job is to monitor system health and act on infrastructure issues using actions provided to you. 
You have access to REAL-TIME tools for Kubernetes (via MCP) and Local Docker containers.

CRITICAL TONE REQUIREMENT:
Anda mesti membalas menggunakan Bahasa Melayu yang santai dan sopan (friendly but professional). 
Gunakan nada seorang jurutera yang berpengalaman tapi mesra. Elakkan penggunaan slang Gen Z yang berlebihan.
Sentiasa hormati Operator.

VERY IMPORTANT:
- Be EXTREMELY concise.
- ONLY answer what the user asks. 
- FORMATTING: Every time you list containers or pods, you MUST use a clean Bullet List with **Bold Headers**. Do NOT use Markdown Tables.

When diagnosing, follow this flow:
1. Examine the provided context silently.
2. Call an action if needed to fix an issue or gather more data.
3. Once you have the result, explain the situation and recommended next steps.`

// NewGeminiAgent sets up the generative model with MCP integration
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
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(systemPrompt),
		},
	}

	// 1. Initialize MCP Manager
	mcpMgr, err := mcp.NewMCPManager()
	if err != nil {
		slog.Warn("MCP server unavailable — running in local-only mode", "error", err)
	}

	// 2. Register Tools
	var toolList []*genai.FunctionDeclaration
	
	// Add Local Tools
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
		client:     client,
		model:      model,
		session:    session,
		mcpManager: mcpMgr,
	}, nil
}

// ConvertMCPSchemaToGenai is a helper to bridge MCP JSON Schema to Gemini Schema
func ConvertMCPSchemaToGenai(mcpSchema interface{}) *genai.Schema {
	// For now, we perform a basic mapping. 
	// We convert the struct to a map first for easier processing.
	var schemaMap map[string]interface{}
	// mcp.ToolInputSchema should be marshalable
	// Note: In real production code, you'd handle this error
	data, _ := json.Marshal(mcpSchema)
	json.Unmarshal(data, &schemaMap)

	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: make(map[string]*genai.Schema),
	}

	if props, ok := schemaMap["properties"].(map[string]interface{}); ok {
		for name, p := range props {
			pMap := p.(map[string]interface{})
			propSchema := &genai.Schema{}
			
			// Map types
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

// ProcessRequest handles the user query and automatic tool execution
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

	// 3. Handle Tool Calls (The Agentic Loop)
	for {
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

// dispatchTool routes a Gemini function call to the correct backend.
// Local Docker operations run through the actions package; everything else
// falls through to the MCP manager (Kubernetes tools).
//
// Destructive operations are blocked unless AI_DESTRUCTIVE_ALLOWED=true.
// In safe mode the AI is forced to route those actions through HITL buttons
// (via ExtractIntents on its text response) instead of executing them directly.
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
// The `evidence` argument is a pre-assembled bundle from the caller containing
// the post-mortem inspect block, recent system metrics, and the log tail. The
// prompt instructs the model to reason across all three rather than skim logs.
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

// send is a small helper that wraps session.SendMessage with retry/backoff so
// every diagnostic path automatically survives transient upstream errors.
func (a *GeminiAgent) send(ctx context.Context, prompt string) (*genai.GenerateContentResponse, error) {
	var resp *genai.GenerateContentResponse
	err := withRetry(ctx, func() error {
		r, e := a.session.SendMessage(ctx, genai.Text(prompt))
		resp = r
		return e
	})
	return resp, err
}

// extractText is a helper to get text from genai.Response
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
		a.client.Close()
	}
	if a.mcpManager != nil {
		a.mcpManager.Close()
	}
}

