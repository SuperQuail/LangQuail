package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

type nodeFailureState struct {
	Messages []llm.Message
	Written  bool
}

type nodeFailureProvider struct {
	name     string
	response llm.Response
	err      error
	requests []llm.Request
}

func (p *nodeFailureProvider) Name() string {
	return p.name
}

func (p *nodeFailureProvider) Chat(_ context.Context, request llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, request)
	if p.err != nil {
		return llm.Response{}, p.err
	}
	if p.response.ID != "" || p.response.Text != "" || len(p.response.ToolCalls) > 0 {
		return p.response, nil
	}
	return llm.Response{
		ID:      "resp_node_failure_contract",
		Model:   request.Model,
		Message: llm.Assistant("ok"),
		Text:    "ok",
	}, nil
}

func TestLLMNodeFailsWithoutMessageReader(t *testing.T) {
	provider := &nodeFailureProvider{name: "fake"}
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{})
	requireLLMNodeFailure(t, result, err, "message reader is required")
	if len(provider.requests) != 0 {
		t.Fatalf("provider requests = %#v, want none", provider.requests)
	}
}

func TestLLMNodeFailsWhenProviderIsMissing(t *testing.T) {
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(),
		Provider:  "missing",
		Model:     "test-model",
		Messages: llm.MessagePolicy[nodeFailureState]{
			Read: func(ctx context.Context, state nodeFailureState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{})
	requireLLMNodeFailure(t, result, err, `provider "missing" is not registered`)
}

func TestLLMNodeFailsWhenStreamingIsUnsupported(t *testing.T) {
	provider := &nodeFailureProvider{name: "fake"}
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Stream:    true,
		Messages: llm.MessagePolicy[nodeFailureState]{
			Read: func(ctx context.Context, state nodeFailureState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{})
	requireLLMNodeFailure(t, result, err, "provider does not support streaming")
	if len(provider.requests) != 0 {
		t.Fatalf("provider requests = %#v, want none", provider.requests)
	}
}

func TestLLMNodeProviderErrorEmitsFailureEvent(t *testing.T) {
	wantErr := errors.New("provider failed")
	provider := &nodeFailureProvider{name: "fake", err: wantErr}
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Messages: llm.MessagePolicy[nodeFailureState]{
			Read: func(ctx context.Context, state nodeFailureState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{
		Messages: []llm.Message{llm.User("hello")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke() error = %v, want %v", err, wantErr)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
	event := requireEvent(t, result.Events, trace.EventLLMFailed)
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode llm.failed payload: %v", err)
	}
	if payload["error"] != "provider failed" {
		t.Fatalf("llm.failed payload = %#v", payload)
	}
}

func TestLLMNodePropagatesMessageWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	provider := &nodeFailureProvider{name: "fake"}
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Messages: llm.MessagePolicy[nodeFailureState]{
			Read: func(ctx context.Context, state nodeFailureState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state nodeFailureState, write llm.MessageWrite) (nodeFailureState, error) {
				return state, wantErr
			},
		},
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{
		Messages: []llm.Message{llm.User("hello")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke() error = %v, want %v", err, wantErr)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
	requireEvent(t, result.Events, trace.EventLLMCompleted)
}

func TestLLMNodePropagatesOutputError(t *testing.T) {
	wantErr := errors.New("output failed")
	provider := &nodeFailureProvider{name: "fake"}
	runner := newLLMFailureRunner(t, llm.NodeSpec[nodeFailureState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Messages: llm.MessagePolicy[nodeFailureState]{
			Read: func(ctx context.Context, state nodeFailureState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state nodeFailureState, write llm.MessageWrite) (nodeFailureState, error) {
				state.Written = true
				return state, nil
			},
		},
		Output: func(ctx context.Context, state nodeFailureState, response llm.Response) (graph.Command[nodeFailureState], error) {
			return graph.Noop[nodeFailureState](), wantErr
		},
	})

	result, err := runner.Invoke(context.Background(), nodeFailureState{
		Messages: []llm.Message{llm.User("hello")},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke() error = %v, want %v", err, wantErr)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
	requireEvent(t, result.Events, trace.EventMessageWritten)
}

func newLLMFailureRunner(t *testing.T, spec llm.NodeSpec[nodeFailureState]) *lqruntime.Runner[nodeFailureState] {
	t.Helper()
	g := graph.NewStateGraph[nodeFailureState]("llm.node.failure.contract")
	g.Node(llm.Node("answer", spec))
	g.Start("answer")
	g.Finish("answer")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner
}

func requireLLMNodeFailure(t *testing.T, result *lqruntime.Result[nodeFailureState], err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Invoke() error = %v, want %q", err, want)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
}

func requireEvent(t *testing.T, events []trace.Event, eventType string) trace.Event {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("event %s not found in %#v", eventType, events)
	return trace.Event{}
}
