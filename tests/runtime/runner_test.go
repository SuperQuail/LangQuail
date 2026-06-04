package runtime_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

type runtimeState struct {
	Count int
	Path  []string
}

func TestInvokeLinearFlow(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.linear")
	g.Step("a", appendNode("a", 1))
	g.Step("b", appendNode("b", 1))
	g.Flow("a", "b")
	g.Start("a")
	g.Finish("b")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if result.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if result.State.Count != 2 || !reflect.DeepEqual(result.State.Path, []string{"a", "b"}) {
		t.Fatalf("state = %#v", result.State)
	}
	if len(result.Checkpoints) != 2 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
	want := []string{
		trace.EventRunStarted,
		trace.EventNodeStarted,
		trace.EventNodeCompleted,
		trace.EventCheckpointSaved,
		trace.EventEdgeSelected,
		trace.EventNodeStarted,
		trace.EventNodeCompleted,
		trace.EventCheckpointSaved,
		trace.EventRunCompleted,
	}
	if got := eventTypes(result.Events); !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %#v", got)
	}
}

func TestInvokeLoopWithOtherwise(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.loop")
	g.Step("tick", appendNode("tick", 1))
	g.Step("done", appendNode("done", 0))
	g.Route("tick").
		When(func(ctx context.Context, state runtimeState) (bool, error) {
			return state.Count < 3, nil
		}, "tick").
		Otherwise("done")
	g.Start("tick")
	g.Finish("done")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if result.State.Count != 3 {
		t.Fatalf("Count = %d", result.State.Count)
	}
	if !reflect.DeepEqual(result.State.Path, []string{"tick", "tick", "tick", "done"}) {
		t.Fatalf("Path = %#v", result.State.Path)
	}
	if len(result.Checkpoints) != 4 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func TestInvokeFailsWhenMaxStepsExceeded(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.max_steps")
	g.Step("tick", appendNode("tick", 1))
	g.Route("tick").Otherwise("tick")
	g.Start("tick")

	runner, err := lqruntime.NewRunner(g, lqruntime.WithMaxSteps[runtimeState](3))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err == nil || !strings.Contains(err.Error(), "runtime: max steps exceeded") {
		t.Fatalf("Invoke() error = %v, want max steps exceeded", err)
	}
	if result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if result.State.Count != 3 {
		t.Fatalf("Count = %d", result.State.Count)
	}
	if len(result.Checkpoints) != 3 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func TestInvokeFailsWhenNodeHasNoRouteToContinue(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.no_route")
	g.Step("a", appendNode("a", 1))
	g.Start("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err == nil || !strings.Contains(err.Error(), "has no route to continue") {
		t.Fatalf("Invoke() error = %v, want no route", err)
	}
	if result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if result.State.Count != 1 {
		t.Fatalf("Count = %d", result.State.Count)
	}
	if len(result.Checkpoints) != 1 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func TestNewRunnerRejectsNegativeMaxSteps(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.negative_max_steps")
	g.Step("a", appendNode("a", 1))
	g.Start("a")
	g.Finish("a")

	runner, err := lqruntime.NewRunner(g, lqruntime.WithMaxSteps[runtimeState](-1))
	if err == nil || !strings.Contains(err.Error(), "max steps cannot be negative") {
		t.Fatalf("NewRunner() error = %v, want negative max steps", err)
	}
	if runner != nil {
		t.Fatalf("runner = %#v, want nil", runner)
	}
}

func TestCommandGotoOverridesFixedEdge(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.goto")
	g.Step("a", func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		state.Path = append(state.Path, "a")
		return graph.UpdateAndGoto(state, "c"), nil
	})
	g.Step("b", appendNode("b", 1))
	g.Step("c", appendNode("c", 1))
	g.Edge("a", "b")
	g.Start("a")
	g.Finish("c")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if !reflect.DeepEqual(result.State.Path, []string{"a", "c"}) {
		t.Fatalf("Path = %#v", result.State.Path)
	}
}

func TestCommandEndCompletesRun(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.end")
	g.Step("a", func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		state.Path = append(state.Path, "a")
		return graph.Command[runtimeState]{Update: &state, End: true}, nil
	})
	g.Step("b", appendNode("b", 1))
	g.Edge("a", "b")
	g.Start("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !reflect.DeepEqual(result.State.Path, []string{"a"}) {
		t.Fatalf("Path = %#v", result.State.Path)
	}
}

func TestNodeErrorFailsRun(t *testing.T) {
	wantErr := errors.New("boom")
	g := graph.NewStateGraph[runtimeState]("runtime.fail")
	g.Step("a", func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		return graph.Noop[runtimeState](), wantErr
	})
	g.Start("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if len(result.Checkpoints) != 0 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func TestContextCancelMarksRunCancelled(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.cancel")
	g.Step("a", appendNode("a", 1))
	g.Start("a")
	g.Finish("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := runner.Invoke(ctx, runtimeState{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.Status != lqruntime.StatusCancelled {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if len(result.Checkpoints) != 0 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func appendNode(name string, delta int) graph.NodeFunc[runtimeState] {
	return func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		state.Count += delta
		state.Path = append(state.Path, name)
		return graph.Update(state), nil
	}
}

func eventTypes(events []trace.Event) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
