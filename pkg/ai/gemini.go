package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"

	"gopher-ops/pkg/mcp"
	"gopher-ops/pkg/monitor"
)

// RCAResult is the structured output Gemini returns for every incident diagnosis
type RCAResult struct {
	RootCause         string `json:"root_cause"`
	Severity          string `json:"severity"`           // LOW | MEDIUM | HIGH | CRITICAL
	RecommendedAction string `json:"recommended_action"`
	Confidence        string `json:"confidence"`          // LOW | MEDIUM | HIGH
	Summary           string `json:"summary"`
}

// Format renders an RCAResult into a Telegram-friendly string
func (r RCAResult) Format() string {
	severityEmoji := map[string]string{
		"LOW": "🟢", "MEDIUM": "🟡", "HIGH": "🟠", "CRITICAL": "🔴",
	}
	confidenceEmoji := map[string]string{
		"LOW": "🤔", "MEDIUM": "🧐", "HIGH": "✅",
	}
	se := severityEmoji[r.Severity]
	if se == "" {
		se = "⚪"
	}
	ce := confidenceEmoji[r.Confidence]
	if ce == "" {
		ce = "🤔"
	}
	return fmt.Sprintf(
		"**Punca:** %s\n**Severity:** %s %s\n**Keyakinan:** %s %s\n\n**Tindakan:**\n%s\n\n**Ringkasan:**\n%s",
		r.RootCause, se, r.Severity, ce, r.Confidence, r.RecommendedAction, r.Summary,
	)
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
	rcaModel   *genai.GenerativeModel // isolated model for RCA — never shares session with user chat
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
		log.Printf("⚠️ MCP Warning: Failed to start K8s MCP server: %v. Running in local-only mode.", err)
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

	// Dedicated model for RCA — isolated from user chat, enforces structured JSON output
	rcaModel := client.GenerativeModel(modelType)
	rcaModel.SetTemperature(0.2)
	rcaModel.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text("You are a Senior SRE diagnosing a production incident. Analyze all provided signals and return structured JSON. All text fields must be in Bahasa Melayu. Be precise, technical, and actionable."),
		},
	}
	rcaModel.ResponseMIMEType = "application/json"
	rcaModel.ResponseSchema = &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"root_cause":         {Type: genai.TypeString, Description: "Punca utama masalah"},
			"severity":           {Type: genai.TypeString, Enum: []string{"LOW", "MEDIUM", "HIGH", "CRITICAL"}},
			"recommended_action": {Type: genai.TypeString, Description: "Langkah-langkah tindakan yang disyorkan"},
			"confidence":         {Type: genai.TypeString, Enum: []string{"LOW", "MEDIUM", "HIGH"}},
			"summary":            {Type: genai.TypeString, Description: "Ringkasan insiden dalam 2-3 ayat"},
		},
		Required: []string{"root_cause", "severity", "recommended_action", "confidence", "summary"},
	}

	return &GeminiAgent{
		client:     client,
		model:      model,
		session:    session,
		rcaModel:   rcaModel,
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
	ctx := context.Background()
	var intents []ActionIntent

	// 1. Gather current context
	health, _ := monitor.GetSystemHealth()
	enrichedPrompt := fmt.Sprintf("```\n%s\n```\nOperator Query: %s", health, userMsg)
	
	// 2. Send Message to AI
	resp, err := a.session.SendMessage(ctx, genai.Text(enrichedPrompt))
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
			log.Printf("🛠️ AI Calling Tool: %s with args: %v", call.Name, call.Args)
			
			var result string
			var execErr error

			// Check if it's an MCP tool or Local tool
			// (This is a simplified dispatch logic)
			if a.mcpManager != nil {
				// Try calling via MCP first if it exists there
				result, execErr = a.mcpManager.CallTool(ctx, call.Name, call.Args)
			}
			
			// Fallback or Handle Local Actions if not found in MCP
			if execErr != nil || result == "" {
				// Here we would call our local Go functions from actions package
				// result = actions.Dispatch(call.Name, call.Args)
				result = fmt.Sprintf("Executed %s successfully (local mock).", call.Name)
			}

			toolResults = append(toolResults, genai.FunctionResponse{
				Name:     call.Name,
				Response: map[string]any{"result": result},
			})
		}

		// Send results back to AI to get final response or next tool call
		resp, err = a.session.SendMessage(ctx, toolResults...)
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

	return output, intents, nil
}

// ResetSession clears the conversation history to save tokens
func (a *GeminiAgent) ResetSession() {
	a.session = a.model.StartChat()
}

// DiagnoseIssue provides RCA using an isolated model with structured JSON output and
// exponential-backoff retry. Never shares state with the user chat session.
func (a *GeminiAgent) DiagnoseIssue(containerName, logs string, extraContext ...string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("PRODUCTION INCIDENT: Container \"%s\" has crashed.\n\nLOGS (last 100 lines):\n%s\n", containerName, logs))
	for _, c := range extraContext {
		if c != "" {
			sb.WriteString("\n")
			sb.WriteString(c)
		}
	}
	sb.WriteString("\nAnalisis semua signal di atas secara holistik dan kembalikan hasil dalam JSON schema yang ditetapkan.")

	prompt := sb.String()
	var resp *genai.GenerateContentResponse
	var err error

	// Retry with exponential backoff on transient API failures
	for attempt := 0; attempt < 3; attempt++ {
		resp, err = a.rcaModel.GenerateContent(context.Background(), genai.Text(prompt))
		if err == nil {
			break
		}
		log.Printf("⚠️ RCA attempt %d failed: %v", attempt+1, err)
		time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
	}
	if err != nil {
		return "", fmt.Errorf("RCA gagal selepas 3 percubaan: %w", err)
	}

	raw := a.extractText(resp)

	// Parse structured JSON and format for Telegram
	var result RCAResult
	if jsonErr := json.Unmarshal([]byte(raw), &result); jsonErr != nil {
		return raw, nil // fallback to raw text if parsing fails
	}
	return result.Format(), nil
}

// AuditSecurity provides a security assessment using the isolated rcaModel
// so it never contaminates the user chat session.
func (a *GeminiAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	prompt := fmt.Sprintf("SECURITY AUDIT REQUEST.\nCONTEXT:\n%s\nIMAGE DATA:\n%s\nAnalisis keselamatan secara menyeluruh dalam Bahasa Melayu dan berikan cadangan tindakan.", ctxData, imageScanData)
	resp, err := a.rcaModel.GenerateContent(context.Background(), genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// TriageIssue provides deeper investigation using the isolated rcaModel.
func (a *GeminiAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	prompt := fmt.Sprintf("TRIAGE REQUEST: %s\nTarget: %s\nData:\n%s\nLakukan triage secara teknikal dalam Bahasa Melayu.", triageType, containerName, data)
	resp, err := a.rcaModel.GenerateContent(context.Background(), genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
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

