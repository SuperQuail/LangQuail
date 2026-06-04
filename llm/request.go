package llm

import "encoding/json"

type ToolChoice string

const (
	ToolChoiceAuto     ToolChoice = "auto"
	ToolChoiceNone     ToolChoice = "none"
	ToolChoiceRequired ToolChoice = "required"
)

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ReasoningConfig struct {
	Enable                  *bool  `json:"enable,omitempty"`
	Effort                  string `json:"effort,omitempty"`
	Summary                 string `json:"summary,omitempty"`
	Display                 string `json:"display,omitempty"`
	IncludeEncryptedContent bool   `json:"include_encrypted_content,omitempty"`
}

type Request struct {
	Provider    string            `json:"provider,omitempty"`
	Model       string            `json:"model"`
	Messages    []Message         `json:"messages"`
	Tools       []ToolSpec        `json:"tools,omitempty"`
	ToolChoice  ToolChoice        `json:"tool_choice,omitempty"`
	MaxTokens   int64             `json:"max_tokens,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
	Reasoning   *ReasoningConfig  `json:"reasoning,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}
