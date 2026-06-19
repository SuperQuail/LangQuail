package trace

import (
	"context"
	"encoding/json"
	"time"
)

const (
	EventRunStarted       = "run.started"
	EventRunCompleted     = "run.completed"
	EventRunFailed        = "run.failed"
	EventRunCancelled     = "run.cancelled"
	EventRunInterrupted   = "run.interrupted"
	EventRunResumed       = "run.resumed"
	EventNodeStarted      = "node.started"
	EventNodeCompleted    = "node.completed"
	EventNodeFailed       = "node.failed"
	EventEdgeSelected     = "edge.selected"
	EventCheckpointSaved  = "checkpoint.saved"
	EventPromptRendered   = "prompt.rendered"
	EventPromptEstimated  = "prompt.estimated"
	EventPromptAdjusted   = "prompt.adjusted"
	EventLLMStarted       = "llm.started"
	EventLLMDelta         = "llm.delta"
	EventLLMCompleted     = "llm.completed"
	EventLLMFailed        = "llm.failed"
	EventToolStarted      = "tool.started"
	EventToolProgress     = "tool.progress"
	EventToolCompleted    = "tool.completed"
	EventToolFailed       = "tool.failed"
	EventMessageRead      = "message.read"
	EventMessageWritten   = "message.written"
	EventInterruptCreated = "interrupt.created"
)

type EmitFunc func(context.Context, string, any) (Event, error)

type emitterContextKey struct{}
type eventContextKey struct{}

func WithEmitter(ctx context.Context, emit EmitFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, emitterContextKey{}, emit)
}

func Emit(ctx context.Context, eventType string, payload any) (Event, error) {
	if ctx == nil {
		return Event{}, nil
	}
	emit, ok := ctx.Value(emitterContextKey{}).(EmitFunc)
	if !ok || emit == nil {
		return Event{}, nil
	}
	return emit(ctx, eventType, payload)
}

func WithEventContext(ctx context.Context, eventContext *EventContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if eventContext == nil {
		return ctx
	}
	return context.WithValue(ctx, eventContextKey{}, eventContext)
}

func EventContextFromContext(ctx context.Context) (*EventContext, bool) {
	if ctx == nil {
		return nil, false
	}
	eventContext, ok := ctx.Value(eventContextKey{}).(*EventContext)
	return eventContext, ok && eventContext != nil
}

type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	ProjectID  string          `json:"project_id,omitempty"`
	WorkflowID string          `json:"workflow_id"`
	SessionID  string          `json:"session_id,omitempty"`
	RunID      string          `json:"run_id"`
	ParentID   string          `json:"parent_id,omitempty"`
	NodeID     string          `json:"node_id,omitempty"`
	Sequence   int64           `json:"sequence"`
	Time       time.Time       `json:"time"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Context    *EventContext   `json:"context,omitempty"`
}

type EventContext struct {
	Current ContextSnapshot `json:"current"`
	Change  *ContextChange  `json:"change,omitempty"`
}

type ContextSnapshot struct {
	State      json.RawMessage `json:"state,omitempty"`
	Messages   json.RawMessage `json:"messages,omitempty"`
	Prompt     json.RawMessage `json:"prompt,omitempty"`
	LLMRequest json.RawMessage `json:"llm_request,omitempty"`
	ToolCall   json.RawMessage `json:"tool_call,omitempty"`
	ToolResult json.RawMessage `json:"tool_result,omitempty"`
}

type ContextChange struct {
	Before json.RawMessage `json:"before,omitempty"`
	After  json.RawMessage `json:"after,omitempty"`
	Ops    json.RawMessage `json:"ops,omitempty"`
}

func Payload(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"payload_error":"marshal_failed"}`)
	}
	return bytes
}
