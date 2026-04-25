package ai

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	
	"gopher-ops/pkg/monitor"
)

// ActionIntent structured data for hitting the HITL (Human-in-the-loop)
type ActionIntent struct {
	Action string
	Target string
}

type GeminiAgent struct {
	client  *genai.Client
	model   *genai.GenerativeModel
	session *genai.ChatSession
}

const systemPrompt = `You are a Senior SRE & AI Automation Engineer named "Gopher-Ops".
Your job is to monitor system health and act on infrastructure issues using actions provided to you. We are analyzing a live container environment alongside REAL memory & CPU load data from the host.

CRITICAL TONE REQUIREMENT:
Anda mesti membalas menggunakan Bahasa Melayu yang santai dan sopan (friendly but professional). 
Gunakan nada seorang jurutera yang berpengalaman tapi mesra. Elakkan penggunaan slang Gen Z yang berlebihan.
Boleh gunakan emoji yang bersesuaian tapi jangan melampau. Sentiasa hormati Operator.

VERY IMPORTANT:
- Be EXTREMELY concise.
- ONLY answer what the user asks. 
- FORMATTING: Every time you list containers or provide a status report, you MUST use a clean Bullet List with **Bold Headers**. Do NOT use Markdown Tables as they look messy on mobile.

If asked for a container list or status:
1. Use a Bullet List for containers. Format: 
   - **[ID] Name** | State: [State] | Status: [Status]
2. Use Bold Bullet Points for System Metrics (CPU/RAM).
3. Do NOT include containers that were not explicitly mentioned or relevant unless "list all" is requested.

When diagnosing, follow this flow:
1. Examine the provided container states and the Host Metrics (CPU/RAM) silently.
2. Answer the user's specific question directly based on the data. Only sound alarmed if CPU is >80% or RAM used is >90% of total.
3. Call an action if requested or if needed to fix an issue. 

👉 **CRITICAL: AGENTIC ACTIONS FORMAT** 👈
If you want to perform an action on a container (Stop, Start, or Restart), you MUST output the exact function text WITH THE CONTAINER ID INSIDE THE PARENTHESES like this:
- `+"`"+`StopContainer("container_id_here")`+"`"+`
- `+"`"+`StartContainer("container_id_here")`+"`"+`
- `+"`"+`RestartContainer("container_id_here")`+"`"+`
- `+"`"+`ViewLogs("container_id_here")`+"`"+`
- `+"`"+`TerraformApply()`+"`"+`
- `+"`"+`AuditSecurity()`+"`"+`
- `+"`"+`VisualMetrics()`+"`"+`
- `+"`"+`ClearCache()`+"`"+`

Example: If the user says "stop container 0ccfe811", YOUR response must include exactly `+"`"+`StopContainer("0ccfe811")`+"`"+`. Do NOT output `+"`"+`StopContainer()`+"`"+` without the ID.`

// NewGeminiAgent sets up the generative model
func NewGeminiAgent(apiKey string) (*GeminiAgent, error) {
	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}

	modelType := "gemini-2.5-flash-lite"
	model := client.GenerativeModel(modelType)

	// Instruct model directly using SetSystemInstruction if available (otherwise we append to start of context).
	// The current SDK uses this layout for models newer than gemini-pro.
	model.SetTemperature(0.7)
	model.SystemInstruction = &genai.Content{
		Parts: []genai.Part{
			genai.Text(systemPrompt),
		},
	}
	
	session := model.StartChat()

	return &GeminiAgent{
		client:  client,
		model:   model,
		session: session,
	}, nil
}

// Close cleans up
func (a *GeminiAgent) Close() {
	if a.client != nil {
		a.client.Close()
	}
}

// ProcessRequest wraps the user query with system context before querying Gemini
func (a *GeminiAgent) ProcessRequest(userMsg string) (string, []ActionIntent, error) {
	ctx := context.Background()
	var intents []ActionIntent

	// 1. Gather current context limits/metrics
	health, err := monitor.GetSystemHealth()
	if err != nil {
		log.Printf("⚠️ Monitor warning: %v", err)
		health = "System health data currently unavailable due to Docker connection issue."
	}
	
	// Create enriched prompt
	enrichedPrompt := fmt.Sprintf("```\n%s\n```\nOperator Query: %s", health, userMsg)
	
	// 2. Query Model
	resp, err := a.session.SendMessage(ctx, genai.Text(enrichedPrompt))
	if err != nil {
		if strings.Contains(err.Error(), "429") {
			return fmt.Sprintf("🛑 WEH SABAR JAP! Gopher-Ops kena rate-limit (Error 429).\nError Asal Google: %s\nQuota free tier Google dah hit maximum la bro. Tunggu seminit pastu borak balik fr fr! ⏳", err.Error()), intents, nil
		}
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not supported") {
			return fmt.Sprintf("❌ GG BRO! Error 404. Model '%s' Google cakap tak wujud atau tak support untuk akaun API kau bro. \nError Google: %s", "current-model", err.Error()), intents, nil
		}
		return "", intents, err
	}

	// Read response out and prepare a string
	var output string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			output += fmt.Sprintf("%s", part)
		}
	}

	// 3. Extract ActionIntents that the AI has suggested
	intents = ExtractIntents(output)

	log.Printf("[OBSERVABILITY] Request: %s | Delivered successful response.", userMsg)

	return output, intents, nil
}

// ResetSession clears the conversation history to save tokens
func (a *GeminiAgent) ResetSession() {
	a.session = a.model.StartChat()
}

// DiagnoseIssue is a special prompt for Root Cause Analysis
func (a *GeminiAgent) DiagnoseIssue(containerName, logs string) (string, error) {
	ctx := context.Background()
	
	prompt := fmt.Sprintf(`SYSTEM ALERT: Container "%s" has crashed or reported an issue.
LOG DATA:
%s

As a Senior SRE, please analyze these logs and tell me:
1. What is the most likely cause of failure?
2. What are the specific steps to fix it?
Keep your answer concise and friendly in Bahasa Melayu.`, containerName, logs)

	resp, err := a.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", err
	}

	var output string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			output += fmt.Sprintf("%s", part)
		}
	}
	return output, nil
}

// AuditSecurity provides a security assessment
func (a *GeminiAgent) AuditSecurity(ctxData string) (string, error) {
	ctx := context.Background()
	
	prompt := fmt.Sprintf(`SYSTEM SECURITY AUDIT REQUEST.
DATA:
%s

As a Senior Security Engineer, please analyze this infrastructure data and provide:
1. A security score from 1 to 10.
2. List of critical vulnerabilities or misconfigurations.
3. Recommended hardening steps.
Keep your answer professional but friendly in Bahasa Melayu.`, ctxData)

	resp, err := a.model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", err
	}

	var output string
	for _, cand := range resp.Candidates {
		for _, part := range cand.Content.Parts {
			output += fmt.Sprintf("%s", part)
		}
	}
	return output, nil
}
