package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
)

func TestToolNodeRunsContextAdjusterBeforeOutput(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "raw:" + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[failureState]("tool.adjuster")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry: registry,
		Metadata: map[string]string{"phase": "test"},
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

	adjuster := &afterToolAdjuster{}
	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx := tool.WithAdjuster(context.Background(), adjuster)
	result, err := runner.Invoke(ctx, failureState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(adjuster.requests) != 1 {
		t.Fatalf("requests = %#v", adjuster.requests)
	}
	request := adjuster.requests[0]
	if request.NodeID != "run_tool" || request.Metadata["phase"] != "test" || request.Result.Content != "raw:langquail" {
		t.Fatalf("adjuster request = %#v", request)
	}
	if len(result.State.Results) != 1 || result.State.Results[0].Content != "trimmed" || result.State.Results[0].CallID != "call_1" || result.State.Results[0].Name != "lookup" {
		t.Fatalf("results = %#v", result.State.Results)
	}
}

type afterToolAdjuster struct {
	requests []tool.AfterToolRequest
}

func (a *afterToolAdjuster) AfterTool(ctx context.Context, request tool.AfterToolRequest) (tool.AfterToolResult, error) {
	a.requests = append(a.requests, request)
	return tool.AfterToolResult{
		Result: &tool.Result{
			Content: "trimmed",
			Raw:     json.RawMessage(`"trimmed"`),
		},
	}, nil
}
