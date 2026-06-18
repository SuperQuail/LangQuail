package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/trace"
)

func TestToolEventsIncludeCallResultAndAdjustmentContext(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "raw:" + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[failureState]("tool.context.events")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry: registry,
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

	var started trace.Event
	var completed trace.Event
	runner, err := lqruntime.NewRunner(
		g,
		lqruntime.WithEventContext[failureState](lqruntime.EventContextOptions{Enabled: true}),
		lqruntime.WithEventHandler[failureState](func(ctx context.Context, event trace.Event) error {
			switch event.Type {
			case trace.EventToolStarted:
				started = event
			case trace.EventToolCompleted:
				completed = event
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx := tool.WithAdjuster(context.Background(), &afterToolAdjuster{})
	result, err := runner.Invoke(ctx, failureState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.State.Results) != 1 || result.State.Results[0].Content != "trimmed" {
		t.Fatalf("results = %#v", result.State.Results)
	}
	if started.Context == nil || len(started.Context.Current.ToolCall) == 0 {
		t.Fatalf("tool.started context = %#v", started.Context)
	}
	var call tool.Call
	if err := json.Unmarshal(started.Context.Current.ToolCall, &call); err != nil {
		t.Fatalf("decode call: %v", err)
	}
	if call.ID != "call_1" || call.Name != "lookup" {
		t.Fatalf("call = %#v", call)
	}
	if completed.Context == nil || completed.Context.Change == nil || len(completed.Context.Current.ToolResult) == 0 || len(completed.Context.Change.Before) == 0 || len(completed.Context.Change.After) == 0 {
		t.Fatalf("tool.completed context = %#v", completed.Context)
	}
}
