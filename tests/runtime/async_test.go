package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

func TestEventStreamHandleDoesNotBlockWhenFull(t *testing.T) {
	stream := lqruntime.NewEventStream(1)
	defer stream.Close()

	if err := stream.Handle(context.Background(), trace.Event{
		Type:       trace.EventRunStarted,
		WorkflowID: "runtime.async.events",
		RunID:      "run_1",
	}); err != nil {
		t.Fatalf("Handle(first) error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stream.Handle(context.Background(), trace.Event{
			Type:       trace.EventRunStarted,
			WorkflowID: "runtime.async.events",
			RunID:      "run_2",
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Handle(second) error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Handle blocked when stream buffer was full")
	}
	if stream.Dropped() != 1 {
		t.Fatalf("Dropped() = %d, want 1", stream.Dropped())
	}
	event := <-stream.Events()
	if event.RunID != "run_1" {
		t.Fatalf("event = %#v", event)
	}

	stream.Close()
	if err := stream.Handle(context.Background(), trace.Event{RunID: "run_after_close"}); err != nil {
		t.Fatalf("Handle(after close) error = %v", err)
	}
}

func TestInvokeAsyncRunsWorkflowInBackground(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})

	g := graph.NewStateGraph[runtimeState]("runtime.invoke.async")
	g.Step("wait", func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		close(started)
		select {
		case <-unblock:
			state.Count++
			state.Path = append(state.Path, "wait")
			return graph.Update(state), nil
		case <-ctx.Done():
			return graph.Noop[runtimeState](), ctx.Err()
		}
	})
	g.Start("wait")
	g.Finish("wait")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	invocation := runner.InvokeAsync(context.Background(), runtimeState{}, lqruntime.WithRunID("run_async"))

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("async workflow did not start")
	}
	select {
	case result := <-invocation.Done():
		t.Fatalf("InvokeAsync completed before node was unblocked: %#v", result)
	case <-time.After(25 * time.Millisecond):
	}

	close(unblock)
	select {
	case result := <-invocation.Done():
		if result.Error != nil {
			t.Fatalf("InvokeAsync error = %v", result.Error)
		}
		if result.Result == nil || result.Result.Run.ID != "run_async" || result.Result.State.Count != 1 {
			t.Fatalf("InvokeAsync result = %#v", result.Result)
		}
	case <-time.After(time.Second):
		t.Fatal("InvokeAsync did not complete after node was unblocked")
	}
}
