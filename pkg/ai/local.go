package ai

import (
	"context"
	"fmt"
	"log/slog"

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
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()

	// 1. Gather current context limits/metrics
	health, err := monitor.GetSystemHealth()
	if err != nil {
		slog.Warn("Monitor unavailable for local LLM context", "error", err)
		health = "System health data currently unavailable due to Docker connection issue."
	}

	// Create enriched prompt
	enrichedPrompt := fmt.Sprintf("```\n%s\n```\nOperator Query: %s", health, userMsg)

	// Add user message to history
	a.history = append(a.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: enrichedPrompt,
	})

	// 2. Query Model with retry
	var resp openai.ChatCompletionResponse
	err = withRetry(ctx, func() error {
		r, e := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    a.model,
			Messages: a.history,
		})
		resp = r
		return e
	})
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

	slog.Info("Local LLM request processed", "model", a.model)

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

// IsOffline always returns false for LocalAgent — the FallbackAgent wrapper
// is what tracks degraded state. Provided so LocalAgent satisfies AIAgent.
func (a *LocalAgent) IsOffline() bool { return false }

// DiagnoseIssue performs deep root-cause analysis via the local LLM. The
// `evidence` argument should already contain the inspect post-mortem, recent
// system metrics, and the log tail.
func (a *LocalAgent) DiagnoseIssue(containerName, evidence string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()

	prompt := fmt.Sprintf(`SYSTEM ALERT: Container %q failed. Perform a DEEP root-cause analysis.

Evidence blocks provided below:
1. CONTAINER POST-MORTEM (exit code, OOMKilled, restart count, health probes).
2. SYSTEM METRICS (recent host CPU/RAM).
3. CONTAINER LOGS (most recent tail).

Reasoning steps:
- Identify the failure CLASS: OOM, panic/segfault, healthcheck, dependency, configuration, or clean exit.
- Trust the exit code, OOMKilled flag, and signal hints — not just log messages.
- Distinguish ROOT CAUSE from downstream SYMPTOMS.
- If evidence is insufficient, say so.

%s

Reply in Bahasa Melayu using:
- **Failure Class:**
- **Root Cause:**
- **Evidence:**
- **Recommended Action:**`, containerName, evidence)

	var resp openai.ChatCompletionResponse
	err := withRetry(ctx, func() error {
		r, e := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: a.model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
				{Role: openai.ChatMessageRoleUser, Content: prompt},
			},
		})
		resp = r
		return e
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// AuditSecurity provides assessment via local LLM.
func (a *LocalAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	prompt := fmt.Sprintf("SECURITY AUDIT REQUEST.\nCONTAINER DATA:\n%s\nIMAGE VULN DATA:\n%s\n\nPlease provide security score and hardening steps in Bahasa Melayu.", ctxData, imageScanData)

	var resp openai.ChatCompletionResponse
	err := withRetry(ctx, func() error {
		r, e := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: a.model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
				{Role: openai.ChatMessageRoleUser, Content: prompt},
			},
		})
		resp = r
		return e
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

// TriageIssue provides a deeper investigation flow via local LLM.
func (a *LocalAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), aiTimeout)
	defer cancel()
	prompt := fmt.Sprintf("SRE TRIAGE REQUEST.\nTarget: %s\nType: %s\nData:\n%s\n\nPlease provide investigation findings in Bahasa Melayu.", containerName, triageType, data)

	var resp openai.ChatCompletionResponse
	err := withRetry(ctx, func() error {
		r, e := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model: a.model,
			Messages: []openai.ChatCompletionMessage{
				{Role: openai.ChatMessageRoleSystem, Content: a.sysPrompt},
				{Role: openai.ChatMessageRoleUser, Content: prompt},
			},
		})
		resp = r
		return e
	})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}
