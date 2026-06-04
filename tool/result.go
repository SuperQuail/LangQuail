package tool

import (
	"encoding/json"
	"fmt"

	"github.com/superquail/langquail/llm"
)

type Result struct {
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Content string          `json:"content,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func (r Result) Message() llm.Message {
	return llm.ToolResult(r.CallID, r.Content)
}

func resultContent(value any, raw json.RawMessage) string {
	if text, ok := value.(string); ok {
		return text
	}
	if len(raw) == 0 {
		return fmt.Sprint(value)
	}
	return string(raw)
}
