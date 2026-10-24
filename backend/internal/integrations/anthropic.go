package integrations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ErrLLMDisabled is returned when no LLM provider is configured; callers fall
// back to non-AI behaviour (e.g. rule-based reply classification).
var ErrLLMDisabled = errors.New("AI features are disabled: set LLM_PROVIDER=anthropic and LLM_API_KEY")

// ErrRefused is returned when the model declines a request.
var ErrRefused = errors.New("the model declined this request")

// Claude implements LLMProvider with the official Anthropic Go SDK.
type Claude struct {
	client anthropic.Client
	model  string
}

// DefaultClaudeModel is used unless LLM_MODEL overrides it.
const DefaultClaudeModel = "claude-opus-5-5"

func NewClaude(apiKey, model string) *Claude {
	if model == "" {
		model = DefaultClaudeModel
	}
	return &Claude{client: anthropic.NewClient(option.WithAPIKey(apiKey)), model: model}
}

func (c *Claude) Generate(ctx context.Context, req LLMRequest) (LLMResponse, error) {
	model := req.Model
	if model == "" {
		model = c.model
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		// Short, high-volume tasks (one-line openers, reply labels): low effort
		// keeps latency and cost down without hurting quality.
		OutputConfig: anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffortLow},
	}
	if req.JSONSchema != nil {
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: req.JSONSchema}
	}
	var msgs []anthropic.MessageParam
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			params.System = append(params.System, anthropic.TextBlockParam{Text: m.Content})
		case "assistant":
			msgs = append(msgs, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		default:
			msgs = append(msgs, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	params.Messages = msgs

	// Server-side refusal fallback: a declined request is re-served by
	// Anthropic's recommended fallback model inside the same call.
	resp, err := c.client.Messages.New(ctx, params,
		option.WithHeaderAdd("anthropic-beta", "server-side-fallback-2026-07-01"),
		option.WithJSONSet("fallbacks", "default"),
	)
	if err != nil {
		return LLMResponse{}, fmt.Errorf("claude: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return LLMResponse{}, ErrRefused
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return LLMResponse{
		Content:      strings.TrimSpace(text.String()),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
	}, nil
}

// Disabled is the provider used when no LLM is configured.
type Disabled struct{}

func (Disabled) Generate(context.Context, LLMRequest) (LLMResponse, error) {
	return LLMResponse{}, ErrLLMDisabled
}

// NewLLM picks the configured provider.
func NewLLM(provider, apiKey, model string) (LLMProvider, error) {
	switch provider {
	case "", "none":
		return Disabled{}, nil
	case "anthropic", "claude":
		if apiKey == "" {
			return nil, errors.New("LLM_PROVIDER=anthropic requires LLM_API_KEY")
		}
		return NewClaude(apiKey, model), nil
	}
	return nil, fmt.Errorf("unknown LLM_PROVIDER %q (supported: anthropic, none)", provider)
}

// Enabled reports whether p can actually generate.
func Enabled(p LLMProvider) bool {
	_, off := p.(Disabled)
	return !off
}
