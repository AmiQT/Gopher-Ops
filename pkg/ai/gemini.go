package ai

import (
	"context"
	"fmt"
	"log"
	"regexp"
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

type Agent struct {
	client *genai.Client
	model  *genai.GenerativeModel
	session *genai.ChatSession
}

const systemPrompt = `You are a Senior SRE & AI Automation Engineer named "Gopher-Ops".
Your job is to monitor system health and act on infrastructure issues using actions provided to you. We are analyzing a live container environment alongside REAL memory & CPU load data from the host.

CRITICAL TONE REQUIREMENT:
You MUST reply speaking entirely in informal, Gen Z Malaysian slang (Bahasa Melayu pasar + Gen Z lingo). 
Use words like: bro, weh, siot, gila, ngam, teruk, no cap, fr fr, slay, let him cook, mantap, koyak. 
Never sound like a robot or use formal standard Indonesian. Be hyped and use relevant emojis like 💀😭🔥🦅✨.

VERY IMPORTANT:
- Be EXTREMELY concise.
- ONLY answer what the user asks. If they ask about RAM, just give RAM. If they ask about a specific container, just read that one container. DO NOT list out all containers or details unless explicitly asked.

When diagnosing, follow this flow:
1. Examine the provided container states and the Host Metrics (CPU/RAM) silently.
2. Answer the user's specific question directly based on the data. Only sound alarmed if CPU is >80% or RAM used is >90% of total.
3. Call an action if requested or if needed to fix an issue. 

👉 **CRITICAL: AGENTIC ACTIONS FORMAT** 👈
If you want to perform an action on a container (Stop, Start, or Restart), you MUST output the exact function text WITH THE CONTAINER ID INSIDE THE PARENTHESES like this:
- `+"`"+`StopContainer("container_id_here")`+"`"+`
- `+"`"+`StartContainer("container_id_here")`+"`"+`
- `+"`"+`RestartContainer("container_id_here")`+"`"+`
- `+"`"+`ClearCache()`+"`"+`

Example: If the user says "stop container 0ccfe811", YOUR response must include exactly `+"`"+`StopContainer("0ccfe811")`+"`"+`. Do NOT output `+"`"+`StopContainer()`+"`"+` without the ID.`

// NewAgent sets up the generative model
func NewAgent(apiKey string) (*Agent, error) {
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

	return &Agent{
		client: client,
		model:  model,
		session: session,
	}, nil
}

// Close cleans up
func (a *Agent) Close() {
	if a.client != nil {
		a.client.Close()
	}
}

// ProcessRequest wraps the user query with system context before querying Gemini
func (a *Agent) ProcessRequest(userMsg string) (string, []ActionIntent, error) {
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

	log.Printf("[OBSERVABILITY] Request: %s | Delivered successful response.", userMsg)

	return output, intents, nil
}

// ResetSession clears the conversation history to save tokens
func (a *Agent) ResetSession() {
	a.session = a.model.StartChat()
}
