package hitl_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tests/testutil"
)

type humanState struct {
	Answer string
}

func TestHumanNodeInterruptAndResume(t *testing.T) {
	g := graph.NewStateGraph[humanState]("hitl.human")
	g.Node(hitl.Node("ask", hitl.NodeSpec[humanState]{
		Request: func(ctx context.Context, state humanState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need answer", map[string]string{"question": "Continue?"}), nil
		},
		Output: func(ctx context.Context, state humanState, response hitl.Response) (graph.Command[humanState], error) {
			answer, err := hitl.DecodePayload[string](response)
			if err != nil {
				return graph.Noop[humanState](), err
			}
			state.Answer = answer
			return graph.Update(state), nil
		},
	}))
	g.Start("ask")
	g.Finish("ask")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	interrupted, err := runner.Invoke(context.Background(), humanState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if interrupted.Run.Status != lqruntime.StatusInterrupted {
		t.Fatalf("status = %s", interrupted.Run.Status)
	}

	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Provide("yes"))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", resumed.Run.Status)
	}
	if resumed.State.Answer != "yes" {
		t.Fatalf("Answer = %q", resumed.State.Answer)
	}
}

func TestHumanNodeInvalidResumePayloadFailsRun(t *testing.T) {
	g := graph.NewStateGraph[humanState]("hitl.invalid.payload")
	g.Node(hitl.Node("ask", hitl.NodeSpec[humanState]{
		Request: func(ctx context.Context, state humanState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need answer", nil), nil
		},
		Output: func(ctx context.Context, state humanState, response hitl.Response) (graph.Command[humanState], error) {
			answer, err := hitl.DecodePayload[string](response)
			if err != nil {
				return graph.Noop[humanState](), err
			}
			state.Answer = answer
			return graph.Update(state), nil
		},
	}))
	g.Start("ask")
	g.Finish("ask")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	interrupted, err := runner.Invoke(context.Background(), humanState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Response{
		Decision: hitl.DecisionProvided,
		Payload:  json.RawMessage(`{`),
	})
	if err == nil {
		t.Fatal("Resume() error = nil, want payload decode error")
	}
	if resumed == nil || resumed.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("resumed = %#v", resumed)
	}
}
