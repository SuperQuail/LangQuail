package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tests/testutil"
	"github.com/superquail/langquail/tool"
)

type permissionState struct {
	Calls   []tool.Call
	Results []string
}

func TestToolPermissionInterruptAndResume(t *testing.T) {
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("danger").
		Permission(tool.RequireApproval[lookupInput]("needs approval")).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "approved:" + input.Query, nil
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[permissionState]("tool.permission")
	g.Node(tool.Node("run_tool", tool.NodeSpec[permissionState]{
		Registry: registry,
		Calls: func(ctx context.Context, state permissionState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state permissionState, results []tool.Result) (graph.Command[permissionState], error) {
			for _, result := range results {
				state.Results = append(state.Results, result.Content)
			}
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	initial := permissionState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "danger",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}}
	interrupted, err := runner.Invoke(context.Background(), initial)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if interrupted.Run.Status != lqruntime.StatusInterrupted {
		t.Fatalf("status = %s", interrupted.Run.Status)
	}
	interruptID := testutil.InterruptIDFromEvents(t, interrupted.Events)
	resumed, err := runner.Resume(context.Background(), interruptID, hitl.Approve(nil))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", resumed.Run.Status)
	}
	if len(resumed.State.Results) != 1 || resumed.State.Results[0] != "approved:langquail" {
		t.Fatalf("results = %#v", resumed.State.Results)
	}
}

func TestToolPermissionRejectFailsWithPermissionDenied(t *testing.T) {
	var executed int
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("danger").
		Permission(tool.RequireApproval[lookupInput]("needs approval")).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			executed++
			return "approved:" + input.Query, nil
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[permissionState]("tool.permission.reject")
	g.Node(tool.Node("run_tool", tool.NodeSpec[permissionState]{
		Registry: registry,
		Calls: func(ctx context.Context, state permissionState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state permissionState, results []tool.Result) (graph.Command[permissionState], error) {
			for _, result := range results {
				state.Results = append(state.Results, result.Content)
			}
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	interrupted, err := runner.Invoke(context.Background(), permissionState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "danger",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Reject("no"))
	if !errors.Is(err, tool.ErrPermissionDenied) {
		t.Fatalf("Resume() error = %v, want permission denied", err)
	}
	if resumed == nil || resumed.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("resumed = %#v", resumed)
	}
	if executed != 0 {
		t.Fatalf("executed = %d, want 0", executed)
	}
}

func TestToolPermissionRejectContinueOnErrorReturnsErrorResult(t *testing.T) {
	var executed int
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, string]("danger").
		Permission(tool.RequireApproval[lookupInput]("needs approval")).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			executed++
			return "approved:" + input.Query, nil
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	g := graph.NewStateGraph[permissionState]("tool.permission.reject.continue")
	g.Node(tool.Node("run_tool", tool.NodeSpec[permissionState]{
		Registry:        registry,
		ContinueOnError: true,
		Calls: func(ctx context.Context, state permissionState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state permissionState, results []tool.Result) (graph.Command[permissionState], error) {
			for _, result := range results {
				state.Results = append(state.Results, result.Error)
			}
			return graph.Update(state), nil
		},
	}))
	g.Start("run_tool")
	g.Finish("run_tool")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	interrupted, err := runner.Invoke(context.Background(), permissionState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "danger",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Reject("no"))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", resumed.Run.Status)
	}
	if executed != 0 {
		t.Fatalf("executed = %d, want 0", executed)
	}
	if len(resumed.State.Results) != 1 || !strings.Contains(resumed.State.Results[0], "permission denied") {
		t.Fatalf("results = %#v", resumed.State.Results)
	}
}
