package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/trace"
)

type directNodeState struct {
	Messages []llm.Message
	Writes   []llm.MessageWrite
}

func TestLLMNodeRunDirectlyWritesMessagesAndEmitsTrace(t *testing.T) {
	provider := &directProvider{name: "fake"}
	enable := true
	node := llm.Node("answer", llm.NodeSpec[directNodeState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Tools: []llm.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		ToolChoice: llm.ToolChoiceAuto,
		MaxTokens:  42,
		Reasoning:  &llm.ReasoningConfig{Enable: &enable, Effort: "low"},
		Messages: llm.MessagePolicy[directNodeState]{
			Read: func(ctx context.Context, state directNodeState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state directNodeState, write llm.MessageWrite) (directNodeState, error) {
				state.Writes = append(state.Writes, write)
				state.Messages = append(state.Messages, write.Response.Message)
				return state, nil
			},
		},
	})

	var events []string
	ctx := trace.WithEmitter(context.Background(), func(ctx context.Context, eventType string, payload any) (trace.Event, error) {
		events = append(events, eventType)
		return trace.Event{Type: eventType, Payload: trace.Payload(payload)}, nil
	})
	command, err := node.Run(ctx, directNodeState{
		Messages: []llm.Message{llm.User("hello")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if command.Update == nil {
		t.Fatal("Run() command update is nil")
	}
	if len(command.Update.Messages) != 2 || command.Update.Messages[1].Content != "direct response" {
		t.Fatalf("updated messages = %#v", command.Update.Messages)
	}
	if len(command.Update.Writes) != 1 {
		t.Fatalf("writes = %#v", command.Update.Writes)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %#v", provider.requests)
	}
	request := provider.requests[0]
	if request.Provider != "fake" || request.Model != "test-model" || request.ToolChoice != llm.ToolChoiceAuto || request.MaxTokens != 42 {
		t.Fatalf("request = %#v", request)
	}
	if len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" {
		t.Fatalf("request tools = %#v", request.Tools)
	}
	if request.Reasoning == nil || request.Reasoning.Effort != "low" || request.Reasoning.Enable == nil || !*request.Reasoning.Enable {
		t.Fatalf("request reasoning = %#v", request.Reasoning)
	}
	wantEvents := []string{
		trace.EventMessageRead,
		trace.EventPromptRendered,
		trace.EventLLMStarted,
		trace.EventLLMCompleted,
		trace.EventMessageWritten,
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v", events)
	}
}

func TestLLMNodeRunDirectlyPropagatesEmitterError(t *testing.T) {
	provider := &directProvider{name: "fake"}
	node := llm.Node("answer", llm.NodeSpec[directNodeState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Messages: llm.MessagePolicy[directNodeState]{
			Read: func(ctx context.Context, state directNodeState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	})
	wantErr := errors.New("emit failed")
	ctx := trace.WithEmitter(context.Background(), func(ctx context.Context, eventType string, payload any) (trace.Event, error) {
		return trace.Event{}, wantErr
	})
	command, err := node.Run(ctx, directNodeState{Messages: []llm.Message{llm.User("hello")}})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	if command.Update != nil {
		t.Fatalf("command = %#v", command)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("provider requests = %#v, want none", provider.requests)
	}
}

type directProvider struct {
	name     string
	requests []llm.Request
}

func (p *directProvider) Name() string {
	return p.name
}

func (p *directProvider) Chat(ctx context.Context, request llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, request)
	return llm.Response{
		ID:      "resp_direct",
		Model:   request.Model,
		Message: llm.Assistant("direct response"),
		Text:    "direct response",
		Usage:   llm.Usage{TotalTokens: 3},
	}, nil
}
