package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/trace"
)

func TestToolProgressEventsIncludeIdentityElapsedAndDuration(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			time.Sleep(30 * time.Millisecond)
			return "done:" + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, events, err := runProgressTool(t, registry, 5*time.Millisecond, []tool.Call{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.State.Results) != 1 || result.State.Results[0].CallID != "call_1" {
		t.Fatalf("results = %#v", result.State.Results)
	}

	startedIndex := requireEventIndex(t, events, trace.EventToolStarted)
	progressIndex := requireEventIndex(t, events, trace.EventToolProgress)
	completedIndex := requireEventIndex(t, events, trace.EventToolCompleted)
	if !(startedIndex < progressIndex && progressIndex < completedIndex) {
		t.Fatalf("event order = %#v", eventTypeList(events))
	}

	progress := decodeProgressPayload(t, events[progressIndex])
	if progress.CallID != "call_1" || progress.Name != "lookup" || progress.ElapsedMS < 0 {
		t.Fatalf("progress payload = %#v", progress)
	}

	completed := decodeCompletedPayload(t, events[completedIndex])
	if completed.CallID != "call_1" || completed.Name != "lookup" || completed.DurationMS < progress.ElapsedMS {
		t.Fatalf("completed payload = %#v progress=%#v", completed, progress)
	}
}

func TestToolNodeGeneratesMissingCallIDForProgressEvents(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			time.Sleep(30 * time.Millisecond)
			return "done", nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, events, err := runProgressTool(t, registry, 5*time.Millisecond, []tool.Call{{
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	started := requireEvent(t, events, trace.EventToolStarted)
	var call tool.Call
	if err := json.Unmarshal(started.Payload, &call); err != nil {
		t.Fatalf("decode started call: %v", err)
	}
	if !strings.HasPrefix(call.ID, "call_") {
		t.Fatalf("generated call id = %q", call.ID)
	}

	progress := decodeProgressPayload(t, requireEvent(t, events, trace.EventToolProgress))
	completed := decodeCompletedPayload(t, requireEvent(t, events, trace.EventToolCompleted))
	if progress.CallID != call.ID || completed.CallID != call.ID || result.State.Results[0].CallID != call.ID {
		t.Fatalf("ids started=%q progress=%q completed=%q result=%q", call.ID, progress.CallID, completed.CallID, result.State.Results[0].CallID)
	}
}

func TestToolFailedEventIncludesCallIDAndDuration(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "", errors.New("lookup failed")
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, events, err := runProgressTool(t, registry, 5*time.Millisecond, []tool.Call{{
		ID:        "call_fail",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}})
	if err == nil || !strings.Contains(err.Error(), "lookup failed") {
		t.Fatalf("Invoke() error = %v, want lookup failed", err)
	}

	var failed struct {
		Tool       string `json:"tool"`
		CallID     string `json:"call_id"`
		DurationMS int64  `json:"duration_ms"`
		Error      string `json:"error"`
	}
	event := requireEvent(t, events, trace.EventToolFailed)
	if err := json.Unmarshal(event.Payload, &failed); err != nil {
		t.Fatalf("decode failed payload: %v", err)
	}
	if failed.Tool != "lookup" || failed.CallID != "call_fail" || failed.DurationMS < 0 || !strings.Contains(failed.Error, "lookup failed") {
		t.Fatalf("failed payload = %#v", failed)
	}
}

func TestToolProgressIntervalCanBeDisabled(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			time.Sleep(30 * time.Millisecond)
			return "done", nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, events, err := runProgressTool(t, registry, -1, []tool.Call{{
		ID:        "call_disabled",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	for _, event := range events {
		if event.Type == trace.EventToolProgress {
			t.Fatalf("unexpected progress event: %#v", event)
		}
	}
}

type progressEventPayload struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type completedToolPayload struct {
	CallID     string `json:"call_id"`
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
}

func runProgressTool(t *testing.T, registry *tool.Registry, interval time.Duration, calls []tool.Call) (*lqruntime.Result[failureState], []trace.Event, error) {
	t.Helper()
	g := graph.NewStateGraph[failureState]("tool.progress")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry:         registry,
		ProgressInterval: interval,
		Calls: func(ctx context.Context, state failureState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state failureState, results []tool.Result) (graph.Command[failureState], error) {
			state.Results = append(state.Results, results...)
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	var events []trace.Event
	runner, err := lqruntime.NewRunner(
		g,
		lqruntime.WithEventHandler[failureState](func(ctx context.Context, event trace.Event) error {
			events = append(events, event)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), failureState{Calls: calls})
	return result, events, err
}

func decodeProgressPayload(t *testing.T, event trace.Event) progressEventPayload {
	t.Helper()
	var payload progressEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode progress payload: %v", err)
	}
	return payload
}

func decodeCompletedPayload(t *testing.T, event trace.Event) completedToolPayload {
	t.Helper()
	var payload completedToolPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode completed payload: %v", err)
	}
	return payload
}

func requireEvent(t *testing.T, events []trace.Event, eventType string) trace.Event {
	t.Helper()
	return events[requireEventIndex(t, events, eventType)]
}

func requireEventIndex(t *testing.T, events []trace.Event, eventType string) int {
	t.Helper()
	for i, event := range events {
		if event.Type == eventType {
			return i
		}
	}
	t.Fatalf("event %s not found in %#v", eventType, eventTypeList(events))
	return -1
}

func eventTypeList(events []trace.Event) []string {
	result := make([]string, 0, len(events))
	for _, event := range events {
		result = append(result, event.Type)
	}
	return result
}
