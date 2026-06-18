package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

func TestEventContextDisabledByDefault(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.context.disabled")
	g.Step("start", appendNode("start", 1))
	g.Start("start")
	g.Finish("start")

	var events []trace.Event
	runner, err := lqruntime.NewRunner(g, lqruntime.WithEventHandler[runtimeState](func(ctx context.Context, event trace.Event) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if _, err := runner.Invoke(context.Background(), runtimeState{}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	for _, event := range events {
		if event.Context != nil {
			t.Fatalf("event %s context = %#v, want nil", event.Type, event.Context)
		}
	}
}

func TestEventContextIsEmittedButNotRecorded(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.context.enabled")
	g.Step("start", appendNode("start", 1))
	g.Start("start")
	g.Finish("start")

	recorder := trace.NewMemoryRecorder()
	var live []trace.Event
	runner, err := lqruntime.NewRunner(
		g,
		lqruntime.WithRecorder[runtimeState](recorder),
		lqruntime.WithEventContext[runtimeState](lqruntime.EventContextOptions{Enabled: true}),
		lqruntime.WithEventHandler[runtimeState](func(ctx context.Context, event trace.Event) error {
			live = append(live, event)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	started := requireRuntimeEvent(t, live, trace.EventNodeStarted)
	if started.Context == nil || len(started.Context.Current.State) == 0 {
		t.Fatalf("node.started context = %#v", started.Context)
	}
	var startedState runtimeState
	if err := json.Unmarshal(started.Context.Current.State, &startedState); err != nil {
		t.Fatalf("decode started state: %v", err)
	}
	if startedState.Count != 0 {
		t.Fatalf("started state = %#v", startedState)
	}

	completed := requireRuntimeEvent(t, live, trace.EventNodeCompleted)
	if completed.Context == nil || completed.Context.Change == nil || len(completed.Context.Change.Before) == 0 || len(completed.Context.Change.After) == 0 {
		t.Fatalf("node.completed context = %#v", completed.Context)
	}
	var after runtimeState
	if err := json.Unmarshal(completed.Context.Change.After, &after); err != nil {
		t.Fatalf("decode completed after: %v", err)
	}
	if after.Count != 1 {
		t.Fatalf("completed after = %#v", after)
	}

	for _, event := range result.Events {
		if event.Context != nil {
			t.Fatalf("result event %s context = %#v, want nil", event.Type, event.Context)
		}
	}
	recorded, err := recorder.List(context.Background(), result.Run.ID, 0)
	if err != nil {
		t.Fatalf("recorder.List() error = %v", err)
	}
	for _, event := range recorded {
		if event.Context != nil {
			t.Fatalf("recorded event %s context = %#v, want nil", event.Type, event.Context)
		}
	}
}
