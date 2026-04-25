package ai

import (
	"fmt"
)

// AIAgent is the interface that all AI providers must implement
type AIAgent interface {
	// ProcessRequest takes a user message and returns the LLM response and any extracted actions
	ProcessRequest(userMsg string) (string, []ActionIntent, error)
	// ResetSession clears the chat history
	ResetSession()
	// DiagnoseIssue provides RCA for a container issue
	DiagnoseIssue(containerName, logs string) (string, error)
	// AuditSecurity provides a security assessment
	AuditSecurity(ctxData string) (string, error)
	// Close cleans up any resources
	Close()
}

// Config holds the configuration for the AI agents
type Config struct {
	Provider    string // "gemini" or "local"
	APIKey      string
	BaseURL     string // for local LLM
	ModelName   string // for local LLM (e.g. "google/gemma-3-4b")
	Temperature float32
}

// NewAgent is a factory function that returns the requested AIAgent implementation
func NewAgent(cfg Config) (AIAgent, error) {
	switch cfg.Provider {
	case "gemini":
		return NewGeminiAgent(cfg.APIKey)
	case "local":
		return NewLocalAgent(cfg)
	default:
		return nil, fmt.Errorf("unknown AI provider: %s", cfg.Provider)
	}
}
