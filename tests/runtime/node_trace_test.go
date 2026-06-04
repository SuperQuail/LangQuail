package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

func TestNodeCanEmitTraceEvents(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.node_trace")
	g.Node(graph.NodeSpec[runtimeState]{
		ID:   "emit",
		Kind: graph.NodeKindLLM,
		Run: func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
			if _, err := trace.Emit(ctx, trace.EventLLMStarted, map[string]string{"model": "test"}); err != nil {
				return graph.Noop[runtimeState](), err
			}
			state.Path = append(state.Path, "emit")
			return graph.Update(state), nil
		},
	})
	g.Start("emit")
	g.Finish("emit")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	types := eventTypes(result.Events)
	if !containsEvent(types, trace.EventLLMStarted) {
		t.Fatalf("event types = %#v, want %s", types, trace.EventLLMStarted)
	}
	event := requireRuntimeEvent(t, result.Events, trace.EventLLMStarted)
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["model"] != "test" {
		t.Fatalf("payload = %#v", payload)
	}
}

func requireRuntimeEvent(t *testing.T, events []trace.Event, eventType string) trace.Event {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("event %s not found in %#v", eventType, events)
	return trace.Event{}
}

func containsEvent(types []string, want string) bool {
	for _, eventType := range types {
		if eventType == want {
			return true
		}
	}
	return false
}
