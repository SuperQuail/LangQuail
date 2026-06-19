package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/trace"
)

type parallelInput struct {
	Label      string `json:"label,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	Mode       string `json:"mode,omitempty"`
}

func TestToolNodeMaxConcurrencyRunsCallsInParallelAndPreservesOrder(t *testing.T) {
	var mu sync.Mutex
	active := 0
	maxActive := 0
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[parallelInput, string]("sleep").
		Execute(func(ctx context.Context, input parallelInput) (string, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			defer func() {
				mu.Lock()
				active--
				mu.Unlock()
			}()
			time.Sleep(time.Duration(input.DurationMS) * time.Millisecond)
			return input.Label, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	started := time.Now()
	result, err := invokeParallelTool(t, registry, 2, false, []tool.Call{
		{ID: "call_slow", Name: "sleep", Arguments: json.RawMessage(`{"label":"slow","duration_ms":80}`)},
		{ID: "call_fast", Name: "sleep", Arguments: json.RawMessage(`{"label":"fast","duration_ms":10}`)},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 130*time.Millisecond {
		t.Fatalf("elapsed = %s, want parallel execution below sequential duration", elapsed)
	}
	if maxActive < 2 {
		t.Fatalf("maxActive = %d, want concurrent execution", maxActive)
	}
	if len(result.State.Results) != 2 || result.State.Results[0].Content != "slow" || result.State.Results[1].Content != "fast" {
		t.Fatalf("results = %#v", result.State.Results)
	}
}

func TestToolNodeParallelFailFastCancelsSiblings(t *testing.T) {
	var mu sync.Mutex
	cancelled := false
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[parallelInput, string]("task").
		Execute(func(ctx context.Context, input parallelInput) (string, error) {
			switch input.Mode {
			case "fail":
				time.Sleep(20 * time.Millisecond)
				return "", errors.New("boom")
			case "wait":
				select {
				case <-ctx.Done():
					mu.Lock()
					cancelled = true
					mu.Unlock()
					return "", ctx.Err()
				case <-time.After(500 * time.Millisecond):
					return "not-cancelled", nil
				}
			default:
				return input.Label, nil
			}
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	started := time.Now()
	_, err := invokeParallelTool(t, registry, 2, false, []tool.Call{
		{ID: "call_wait", Name: "task", Arguments: json.RawMessage(`{"mode":"wait"}`)},
		{ID: "call_fail", Name: "task", Arguments: json.RawMessage(`{"mode":"fail"}`)},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Invoke() error = %v, want boom", err)
	}
	if elapsed := time.Since(started); elapsed >= 300*time.Millisecond {
		t.Fatalf("elapsed = %s, sibling was not cancelled promptly", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if !cancelled {
		t.Fatal("waiting sibling did not observe cancellation")
	}
}

func TestToolNodeParallelContinueOnErrorCollectsResults(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[parallelInput, string]("task").
		Execute(func(ctx context.Context, input parallelInput) (string, error) {
			if input.Mode == "fail" {
				return "", errors.New("boom")
			}
			time.Sleep(time.Duration(input.DurationMS) * time.Millisecond)
			return input.Label, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, err := invokeParallelTool(t, registry, 2, true, []tool.Call{
		{ID: "call_fail", Name: "task", Arguments: json.RawMessage(`{"mode":"fail"}`)},
		{ID: "call_ok", Name: "task", Arguments: json.RawMessage(`{"label":"ok","duration_ms":20}`)},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(result.State.Results) != 2 {
		t.Fatalf("results = %#v", result.State.Results)
	}
	if !strings.Contains(result.State.Results[0].Error, "boom") || result.State.Results[1].Content != "ok" {
		t.Fatalf("results = %#v", result.State.Results)
	}
}

func TestToolNodeParallelPermissionPreflightDoesNotExecuteTools(t *testing.T) {
	executed := 0
	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("danger").
		Permission(tool.RequireApproval[lookupInput]("needs approval")).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			executed++
			return "danger", nil
		})); err != nil {
		t.Fatalf("Register(danger) error = %v", err)
	}
	if err := registry.Register(tool.Define[lookupInput, string]("safe").
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			executed++
			return "safe", nil
		})); err != nil {
		t.Fatalf("Register(safe) error = %v", err)
	}

	result, err := invokeParallelTool(t, registry, 2, false, []tool.Call{
		{ID: "call_danger", Name: "danger", Arguments: json.RawMessage(`{"query":"x"}`)},
		{ID: "call_safe", Name: "safe", Arguments: json.RawMessage(`{"query":"x"}`)},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.Status != runtime.StatusInterrupted {
		t.Fatalf("status = %s, want interrupted", result.Run.Status)
	}
	if executed != 0 {
		t.Fatalf("executed = %d, want no tools executed before permission interrupt", executed)
	}
	for _, event := range result.Events {
		if event.Type == trace.EventToolStarted {
			t.Fatalf("unexpected tool start before permission interrupt: %#v", event)
		}
	}
}

func invokeParallelTool(t *testing.T, registry *tool.Registry, maxConcurrency int, continueOnError bool, calls []tool.Call) (*runtime.Result[failureState], error) {
	t.Helper()
	g := graph.NewStateGraph[failureState]("tool.parallel")
	g.Node(tool.Node("run_tool", tool.NodeSpec[failureState]{
		Registry:        registry,
		MaxConcurrency:  maxConcurrency,
		ContinueOnError: continueOnError,
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
	runner, err := runtime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner.Invoke(context.Background(), failureState{Calls: calls})
}
