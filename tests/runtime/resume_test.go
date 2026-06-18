package runtime_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tests/testutil"
	"github.com/superquail/langquail/trace"
)

func TestResumeRecordsRunResumed(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.resume")
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
	interrupted, err := runner.Invoke(context.Background(), runtimeState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Provide(nil))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", resumed.Run.Status)
	}
	if !containsEvent(eventTypes(resumed.Events), trace.EventRunResumed) {
		t.Fatalf("event types = %#v, want run.resumed", eventTypes(resumed.Events))
	}
}

func TestResumeRestoresCheckpointStateRunIdentitySessionAndMetadata(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.resume.continuity")
	g.Step("prepare", func(ctx context.Context, state runtimeState) (graph.Command[runtimeState], error) {
		state.Count = 1
		state.Path = append(state.Path, "prepared")
		return graph.Update(state), nil
	})
	g.Node(hitl.Node("human", hitl.NodeSpec[runtimeState]{
		Request: func(ctx context.Context, state runtimeState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need input", map[string]string{
				"question": "Continue?",
			}), nil
		},
		Output: func(ctx context.Context, state runtimeState, response hitl.Response) (graph.Command[runtimeState], error) {
			answer, err := hitl.DecodePayload[string](response)
			if err != nil {
				return graph.Noop[runtimeState](), err
			}
			state.Path = append(state.Path, "resumed:"+answer)
			return graph.Update(state), nil
		},
	}))
	g.Flow("prepare", "human")
	g.Start("prepare")
	g.Finish("human")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	metadata := map[string]string{"tenant": "acme"}
	interrupted, err := runner.Invoke(context.Background(), runtimeState{},
		lqruntime.WithRunID("run_resume_contract"),
		lqruntime.WithSession("session_1"),
		lqruntime.WithMetadata(metadata),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	metadata["tenant"] = "mutated"
	if interrupted.Run.Status != lqruntime.StatusInterrupted {
		t.Fatalf("interrupted status = %s", interrupted.Run.Status)
	}
	if interrupted.Run.ID != "run_resume_contract" || interrupted.Run.SessionID != "session_1" {
		t.Fatalf("interrupted run = %#v", interrupted.Run)
	}
	if interrupted.Run.Metadata["tenant"] != "acme" {
		t.Fatalf("interrupted metadata = %#v", interrupted.Run.Metadata)
	}
	if len(interrupted.Checkpoints) != 2 {
		t.Fatalf("interrupted checkpoints = %#v", interrupted.Checkpoints)
	}

	resumed, err := runner.Resume(context.Background(), testutil.InterruptIDFromEvents(t, interrupted.Events), hitl.Provide("yes"))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("resumed status = %s", resumed.Run.Status)
	}
	if resumed.Run.ID != interrupted.Run.ID || resumed.Run.WorkflowID != interrupted.Run.WorkflowID {
		t.Fatalf("resumed run identity = %#v, interrupted = %#v", resumed.Run, interrupted.Run)
	}
	if resumed.Run.SessionID != "session_1" {
		t.Fatalf("resumed session = %q", resumed.Run.SessionID)
	}
	if resumed.Run.Metadata["tenant"] != "acme" {
		t.Fatalf("resumed metadata = %#v", resumed.Run.Metadata)
	}
	if resumed.State.Count != 1 || !reflect.DeepEqual(resumed.State.Path, []string{"prepared", "resumed:yes"}) {
		t.Fatalf("resumed state = %#v", resumed.State)
	}
	if len(resumed.Checkpoints) != 3 {
		t.Fatalf("resumed checkpoints = %#v", resumed.Checkpoints)
	}

	event := runResumedEvent(t, resumed.Events)
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode run.resumed payload: %v", err)
	}
	if payload["interrupt_id"] == "" || payload["checkpoint_id"] == "" || payload["resume_node"] != "human" {
		t.Fatalf("run.resumed payload = %#v", payload)
	}
}

func TestResumeFromUsesCallerProvidedState(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.resume.from")
	g.Node(hitl.Node("human", hitl.NodeSpec[runtimeState]{
		Request: func(ctx context.Context, state runtimeState) (hitl.Request, error) {
			return hitl.NewRequest(hitl.RequestKindHumanInput, "need input", nil), nil
		},
		Output: func(ctx context.Context, state runtimeState, response hitl.Response) (graph.Command[runtimeState], error) {
			answer, err := hitl.DecodePayload[string](response)
			if err != nil {
				return graph.Noop[runtimeState](), err
			}
			state.Path = append(state.Path, "resumed:"+answer)
			return graph.Update(state), nil
		},
	}))
	g.Start("human")
	g.Finish("human")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.ResumeFrom(context.Background(), lqruntime.ResumeRequest[runtimeState]{
		Run: lqruntime.Run{
			ID:         "run_saved",
			WorkflowID: "runtime.resume.from",
			SessionID:  "session_saved",
			Metadata:   map[string]string{"tenant": "acme"},
		},
		State:        runtimeState{Count: 7, Path: []string{"saved"}},
		ResumeNode:   "human",
		Response:     hitl.Provide("yes"),
		InterruptID:  "int_saved",
		CheckpointID: "chk_saved",
	})
	if err != nil {
		t.Fatalf("ResumeFrom() error = %v", err)
	}
	if result.Run.Status != lqruntime.StatusCompleted || result.Run.ID != "run_saved" || result.Run.SessionID != "session_saved" {
		t.Fatalf("run = %#v", result.Run)
	}
	if result.Run.Metadata["tenant"] != "acme" {
		t.Fatalf("metadata = %#v", result.Run.Metadata)
	}
	if result.State.Count != 7 || !reflect.DeepEqual(result.State.Path, []string{"saved", "resumed:yes"}) {
		t.Fatalf("state = %#v", result.State)
	}
	if len(result.Checkpoints) != 1 {
		t.Fatalf("checkpoints = %#v", result.Checkpoints)
	}
	event := runResumedEvent(t, result.Events)
	var payload map[string]string
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode run.resumed payload: %v", err)
	}
	if payload["interrupt_id"] != "int_saved" || payload["checkpoint_id"] != "chk_saved" || payload["resume_node"] != "human" {
		t.Fatalf("run.resumed payload = %#v", payload)
	}
}

func runResumedEvent(t *testing.T, events []trace.Event) trace.Event {
	t.Helper()
	for _, event := range events {
		if event.Type == trace.EventRunResumed {
			return event
		}
	}
	t.Fatal("run.resumed event not found")
	return trace.Event{}
}
