package llm

import "encoding/json"

type Response struct {
	ID         string          `json:"id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Message    Message         `json:"message"`
	Text       string          `json:"text,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	Usage      Usage           `json:"usage,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}
