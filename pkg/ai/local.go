package ai

import (
	"context"
	"fmt"
	"log"
	"gopher-ops/pkg/monitor"

	"github.com/sashabaranov/go-openai"
)

type LocalAgent struct {
	client    *openai.Client
	model     string
	history   []openai.ChatCompletionMessage
	sysPrompt string
}

// NewLocalAgent sets up an OpenAI-compatible client for local LLMs
func NewLocalAgent(cfg Config) (*LocalAgent, error) {
	config := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		config.BaseURL = cfg.BaseURL
	}
	
	client := openai.NewClientWithConfig(config)

	agent := &LocalAgent{
		client:    client,
		model:     cfg.ModelName,
		sysPrompt: systemPrompt,
		history:   []openai.ChatCompletionMessage{},
	}

	agent.ResetSession()
	return agent, nil
}

func (a *LocalAgent) ProcessRequest(userMsg string) (string, []ActionIntent, error) {
	ctx := context.Background()

	// 1. Gather current context limits/metrics
	health, err := monitor.GetSystemHealth()
	if err != nil {
		log.Printf("⚠️ Monitor warning: %v", err)
		health = "System health data currently unavailable due to Docker connection issue."
	}

	// Create enriched prompt
	enrichedPrompt := fmt.Sprintf("```\n%s\n```\nOperator Query: %s", health, userMsg)
	
	// Add user message to history
	a.history = append(a.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: enrichedPrompt,
	})

	// 2. Query Model
	resp, err := a.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model:    a.model,
			Messages: a.history,
		},
	)

	if err != nil {
		return "", nil, fmt.Errorf("local LLM error: %v", err)
	}

	output := resp.Choices[0].Message.Content

	// Add assistant response to history
	a.history = append(a.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: output,
	})

	// 3. Extract Actions
	intents := ExtractIntents(output)

	log.Printf("[OBSERVABILITY-LOCAL] Request processed via %s", a.model)

	return output, intents, nil
}

func (a *LocalAgent) ResetSession() {
	a.history = []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: a.sysPrompt,
		},
	}
}

func (a *LocalAgent) Close() {
	// No specific cleanup needed for the openai client
}

// DiagnoseIssue provides RCA via local LLM
func (a *LocalAgent) DiagnoseIssue(containerName, logs string) (string, error) {
	ctx := context.Background()
	prompt := fmt.Sprintf("SYSTEM ALERT: Container \"%s\" issue analysis.\nLOGS:\n%s\n\nPlease provide RCA and fix steps in Bahasa Melayu.", containerName, logs)
	
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// AuditSecurity provides assessment via local LLM
func (a *LocalAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	ctx := context.Background()
	prompt := fmt.Sprintf("SECURITY AUDIT REQUEST.\nCONTAINER DATA:\n%s\nIMAGE VULN DATA:\n%s\n\nPlease provide security score and hardening steps in Bahasa Melayu.", ctxData, imageScanData)
	
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// TriageIssue provides a deeper investigation flow via local LLM
func (a *LocalAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	ctx := context.Background()
	prompt := fmt.Sprintf("SRE TRIAGE REQUEST.\nTarget: %s\nType: %s\nData:\n%s\n\nPlease provide investigation findings in Bahasa Melayu.", containerName, triageType, data)
	
	resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: a.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

