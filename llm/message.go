package llm

import (
	"bytes"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

func System(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

func Developer(content string) Message {
	return Message{Role: RoleDeveloper, Content: content}
}

func User(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

func Assistant(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

func AssistantToolCalls(content string, calls []ToolCall) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: cloneToolCalls(calls)}
}

func ToolResult(toolCallID string, content string) Message {
	return Message{Role: RoleTool, ToolCallID: toolCallID, Content: content}
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = call
		cloned[i].Arguments = json.RawMessage(bytes.Clone(call.Arguments))
	}
	return cloned
}
