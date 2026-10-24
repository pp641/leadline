// Package integrations holds contracts for third-party services that are not
// email transport (which lives in internal/email). Phase 2 uses LLMProvider
// for personalization and reply classification.
package integrations

import "context"

type LLMMessage struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

type LLMRequest struct {
	Model       string
	Messages    []LLMMessage
	MaxTokens   int
	Temperature float64
	// JSONSchema, when set, asks the provider for structured output.
	JSONSchema map[string]any
}

type LLMResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
}

// LLMProvider is implemented per vendor; business logic only sees this.
// Generated outreach must be grounded in stored lead facts and reviewed by a
// user before sending — no fabricated claims or impersonation.
type LLMProvider interface {
	Generate(ctx context.Context, request LLMRequest) (LLMResponse, error)
}
