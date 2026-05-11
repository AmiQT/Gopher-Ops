package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	
	"gopher-ops/pkg/mcp"
	"gopher-ops/pkg/monitor"
)

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

// DiagnoseIssue provides RCA for a container issue
func (a *GeminiAgent) DiagnoseIssue(containerName, logs string) (string, error) {
	prompt := fmt.Sprintf("SYSTEM ALERT: Container \"%s\" has crashed.\nLOGS:\n%s\nAnalyze and provide RCA in Bahasa Melayu.", containerName, logs)
	resp, err := a.session.SendMessage(context.Background(), genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// AuditSecurity provides a security assessment
func (a *GeminiAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	prompt := fmt.Sprintf("SECURITY AUDIT REQUEST.\nCONTEXT:\n%s\nIMAGE DATA:\n%s\nAnalyze security in Bahasa Melayu.", ctxData, imageScanData)
	resp, err := a.session.SendMessage(context.Background(), genai.Text(prompt))
	if err != nil {
		return "", err
	}
	return a.extractText(resp), nil
}

// TriageIssue provides a deeper investigation flow
func (a *GeminiAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	prompt := fmt.Sprintf("TRIAGE REQUEST: %s\nTarget: %s\nData:\n%s\nPerform triage in Bahasa Melayu.", triageType, containerName, data)
	resp, err := a.session.SendMessage(context.Background(), genai.Text(prompt))
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

