package runtime_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tests/testutil"
)

func TestResumeRejectsAlreadyResolvedInterrupt(t *testing.T) {
	runner := newResumeSafetyRunner(t)
	interrupted, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	interruptID := testutil.InterruptIDFromEvents(t, interrupted.Events)

	if _, err := runner.Resume(context.Background(), interruptID, hitl.Provide(nil)); err != nil {
		t.Fatalf("Resume(first) error = %v", err)
	}
	resumed, err := runner.Resume(context.Background(), interruptID, hitl.Provide(nil))
	if err == nil || !strings.Contains(err.Error(), "already resolved") {
		t.Fatalf("Resume(second) error = %v, want already resolved", err)
	}
	if resumed != nil {
		t.Fatalf("Resume(second) result = %#v, want nil", resumed)
	}
}

func TestConcurrentResumeAllowsOnlyOneSuccess(t *testing.T) {
	runner := newResumeSafetyRunner(t)
	interrupted, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	interruptID := testutil.InterruptIDFromEvents(t, interrupted.Events)

	const attempts = 16
	start := make(chan struct{})
	outcomes := make(chan struct {
		result *lqruntime.Result[runtimeState]
		err    error
	}, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := runner.Resume(context.Background(), interruptID, hitl.Provide(nil))
			outcomes <- struct {
				result *lqruntime.Result[runtimeState]
				err    error
			}{result: result, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	successes := 0
	alreadyResolved := 0
	for outcome := range outcomes {
		if outcome.err == nil {
			successes++
			if outcome.result == nil || outcome.result.Run.Status != lqruntime.StatusCompleted {
				t.Fatalf("successful Resume() result = %#v", outcome.result)
			}
			continue
		}
		if strings.Contains(outcome.err.Error(), "already resolved") {
			alreadyResolved++
			if outcome.result != nil {
				t.Fatalf("already resolved result = %#v, want nil", outcome.result)
			}
			continue
		}
		t.Fatalf("Resume() error = %v, want already resolved", outcome.err)
	}
	if successes != 1 || alreadyResolved != attempts-1 {
		t.Fatalf("successes=%d alreadyResolved=%d attempts=%d", successes, alreadyResolved, attempts)
	}
}

func TestResumeRejectsMissingAndUnknownInterruptID(t *testing.T) {
	runner := newResumeSafetyRunner(t)
	tests := []struct {
		name        string
		interruptID string
		want        string
	}{
		{name: "missing", interruptID: "", want: "interrupt id is required"},
		{name: "unknown", interruptID: "int_missing", want: "interrupt not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := runner.Resume(context.Background(), tt.interruptID, hitl.Provide(nil))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Resume() error = %v, want %q", err, tt.want)
			}
			if result != nil {
				t.Fatalf("Resume() result = %#v, want nil", result)
			}
		})
	}
}

func newResumeSafetyRunner(t *testing.T) *lqruntime.Runner[runtimeState] {
	t.Helper()
	g := graph.NewStateGraph[runtimeState]("runtime.resume.safety")
	g.Node(hitl.Node("human", hitl.NodeSpec[runtimeState]{
		Request: func(ctx context.Context, state runtimeState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need input", nil), nil
		},
		Output: func(ctx context.Context, state runtimeState, response hitl.Response) (graph.Command[runtimeState], error) {
			state.Path = append(state.Path, "resumed")
			return graph.Update(state), nil
		},
	}))
	g.Start("human")
	g.Finish("human")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner
}
