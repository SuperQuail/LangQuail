package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
)

type failureState struct {
	Calls   []tool.Call
	Results []tool.Result
}

func TestToolNodeContinueOnErrorReturnsToolResult(t *testing.T) {
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("file_read").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "", errors.New("open missing.go: no such file")
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[failureState]("tool.failure")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry:        registry,
		ContinueOnError: true,
		Calls: func(ctx context.Context, state failureState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state failureState, results []tool.Result) (graph.Command[failureState], error) {
			state.Results = append(state.Results, results...)
			state.Calls = nil
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), failureState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "file_read",
		Arguments: json.RawMessage(`{"query":"trace/recorder.go"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.State.Results) != 1 {
		t.Fatalf("results = %#v", result.State.Results)
	}
	toolResult := result.State.Results[0]
	if toolResult.CallID != "call_1" || toolResult.Name != "file_read" {
		t.Fatalf("result identity = %#v", toolResult)
	}
	if toolResult.Error == "" || !strings.Contains(toolResult.Content, "open missing.go") {
		t.Fatalf("error result = %#v", toolResult)
	}
	if message := toolResult.Message(); message.Content != toolResult.Content {
		t.Fatalf("message content = %q, want %q", message.Content, toolResult.Content)
	}
}

func TestToolNodeFailsOnUnknownTool(t *testing.T) {
	registry := tool.NewRegistry()
	g := graph.NewStateGraph[failureState]("tool.unknown")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry: registry,
		Calls: func(ctx context.Context, state failureState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state failureState, results []tool.Result) (graph.Command[failureState], error) {
			state.Results = append(state.Results, results...)
			state.Calls = nil
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), failureState{Calls: []tool.Call{{
		ID:        "call_missing",
		Name:      "missing",
		Arguments: json.RawMessage(`{}`),
	}}})
	if err == nil || !strings.Contains(err.Error(), `tool "missing" is not registered`) {
		t.Fatalf("Invoke() error = %v, want unknown tool", err)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
}

func TestToolNodeContinueOnErrorHandlesUnknownToolAndBadArguments(t *testing.T) {
	executed := 0
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			executed++
			return "found:" + input.Query, nil
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[failureState]("tool.bad.input")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry:        registry,
		ContinueOnError: true,
		Calls: func(ctx context.Context, state failureState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state failureState, results []tool.Result) (graph.Command[failureState], error) {
			state.Results = append(state.Results, results...)
			state.Calls = nil
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), failureState{Calls: []tool.Call{
		{
			ID:        "call_missing",
			Name:      "missing",
			Arguments: json.RawMessage(`{}`),
		},
		{
			ID:        "call_bad_json",
			Name:      "lookup",
			Arguments: json.RawMessage(`{`),
		},
	}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if executed != 0 {
		t.Fatalf("executed = %d, want 0", executed)
	}
	if len(result.State.Results) != 2 {
		t.Fatalf("results = %#v", result.State.Results)
	}
	if result.State.Results[0].CallID != "call_missing" || !strings.Contains(result.State.Results[0].Error, `tool "missing" is not registered`) {
		t.Fatalf("unknown tool result = %#v", result.State.Results[0])
	}
	if result.State.Results[1].CallID != "call_bad_json" || !strings.Contains(result.State.Results[1].Error, "unexpected end of JSON input") {
		t.Fatalf("bad JSON result = %#v", result.State.Results[1])
	}
}

func TestToolNodeErrorHandlerCanReturnCommand(t *testing.T) {
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("file_read").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "", errors.New("open missing.go: no such file")
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[failureState]("tool.error.handler")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry: registry,
		Calls: func(ctx context.Context, state failureState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Error: func(ctx context.Context, state failureState, call tool.Call, err error) (graph.Command[failureState], error) {
			state.Results = append(state.Results, tool.ErrorResult(call, err))
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), failureState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "file_read",
		Arguments: json.RawMessage(`{"query":"trace/recorder.go"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.State.Results) != 1 || !strings.Contains(result.State.Results[0].Error, "missing.go") {
		t.Fatalf("results = %#v", result.State.Results)
	}
}
