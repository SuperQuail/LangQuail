package llm

import "context"

type StreamHandler func(context.Context, StreamChunk) error

type StreamProvider interface {
	Provider
	ChatStream(context.Context, Request, StreamHandler) (Response, error)
}

type StreamChunk struct {
	Text     string    `json:"text,omitempty"`
	Thinking string    `json:"thinking,omitempty"`
	ToolCall *ToolCall `json:"tool_call,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
	Done     bool      `json:"done,omitempty"`
}
