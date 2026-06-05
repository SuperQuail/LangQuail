package token

import (
	"context"
	"encoding/json"
	"errors"
)

type Source string

const (
	SourceTiktoken  Source = "tiktoken"
	SourceClaudeAPI Source = "claude_api"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type EstimateRequest struct {
	Provider        string            `json:"provider,omitempty"`
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Tools           []ToolSpec        `json:"tools,omitempty"`
	MaxOutputTokens int64             `json:"max_output_tokens,omitempty"`
	ContextLimit    int64             `json:"context_limit,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type Estimate struct {
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	SystemTokens     int64  `json:"system_tokens,omitempty"`
	MessageTokens    int64  `json:"message_tokens,omitempty"`
	ToolSchemaTokens int64  `json:"tool_schema_tokens,omitempty"`
	OverheadTokens   int64  `json:"overhead_tokens,omitempty"`
	ContextLimit     int64  `json:"context_limit,omitempty"`
	MaxOutputTokens  int64  `json:"max_output_tokens,omitempty"`
	RemainingTokens  int64  `json:"remaining_tokens,omitempty"`
	Source           Source `json:"source"`
	Estimated        bool   `json:"estimated"`
}

type Budget struct {
	ContextLimit    int64 `json:"context_limit,omitempty"`
	MaxOutputTokens int64 `json:"max_output_tokens,omitempty"`
	PromptLimit     int64 `json:"prompt_limit,omitempty"`
}

type Estimator interface {
	CountPromptTokens(context.Context, EstimateRequest) (Estimate, error)
}

type estimatorContextKey struct{}

func WithEstimator(ctx context.Context, estimator Estimator) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if estimator == nil {
		return ctx
	}
	return context.WithValue(ctx, estimatorContextKey{}, estimator)
}

func EstimatorFromContext(ctx context.Context) (Estimator, bool) {
	if ctx == nil {
		return nil, false
	}
	estimator, ok := ctx.Value(estimatorContextKey{}).(Estimator)
	return estimator, ok && estimator != nil
}

func finalizeEstimate(estimate Estimate) Estimate {
	if estimate.ContextLimit > 0 {
		estimate.RemainingTokens = estimate.ContextLimit - estimate.InputTokens - estimate.MaxOutputTokens
	}
	return estimate
}

func ValidateRequest(request EstimateRequest) error {
	if request.Model == "" {
		return errors.New("token: model is required")
	}
	return nil
}
