package tool

import (
	"bytes"
	"encoding/json"

	"github.com/superquail/langquail/llm"
)

type Call struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func FromLLMToolCalls(calls []llm.ToolCall) []Call {
	result := make([]Call, 0, len(calls))
	for _, call := range calls {
		result = append(result, Call{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: json.RawMessage(bytes.Clone(call.Arguments)),
		})
	}
	return result
}
