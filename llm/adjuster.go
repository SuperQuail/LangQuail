package llm

import (
	"bytes"
	"context"
	"encoding/json"

	lqprompt "github.com/superquail/langquail/prompt"
	lqtoken "github.com/superquail/langquail/token"
)

type Adjuster interface {
	BeforeLLM(context.Context, BeforeLLMRequest) (BeforeLLMResult, error)
}

type BeforeLLMRequest struct {
	NodeID   string            `json:"node_id"`
	Provider string            `json:"provider,omitempty"`
	Model    string            `json:"model"`
	PromptID string            `json:"prompt_id,omitempty"`
	Messages []Message         `json:"messages"`
	Tools    []ToolSpec        `json:"tools,omitempty"`
	Budget   lqtoken.Budget    `json:"budget,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type BeforeLLMResult struct {
	Messages []Message               `json:"messages,omitempty"`
	Adjusted *lqprompt.CompactResult `json:"adjusted,omitempty"`
}

type adjusterContextKey struct{}

func WithAdjuster(ctx context.Context, adjuster Adjuster) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if adjuster == nil {
		return ctx
	}
	return context.WithValue(ctx, adjusterContextKey{}, adjuster)
}

func AdjusterFromContext(ctx context.Context) (Adjuster, bool) {
	if ctx == nil {
		return nil, false
	}
	adjuster, ok := ctx.Value(adjusterContextKey{}).(Adjuster)
	return adjuster, ok && adjuster != nil
}

func cloneMessages(messages []Message) []Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, message := range messages {
		cloned[i] = cloneMessage(message)
	}
	return cloned
}

func cloneToolSpecs(specs []ToolSpec) []ToolSpec {
	if len(specs) == 0 {
		return nil
	}
	cloned := make([]ToolSpec, len(specs))
	for i, spec := range specs {
		cloned[i] = spec
		cloned[i].InputSchema = json.RawMessage(bytes.Clone(spec.InputSchema))
	}
	return cloned
}
