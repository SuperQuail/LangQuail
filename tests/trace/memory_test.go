package trace_test

import (
	"context"
	"testing"

	"github.com/superquail/langquail/trace"
)

func TestMemoryRecorderSequencesAndReplay(t *testing.T) {
	recorder := trace.NewMemoryRecorder()
	ctx := context.Background()

	first, err := recorder.Record(ctx, trace.Event{Type: trace.EventRunStarted, RunID: "run_1", WorkflowID: "wf"})
	if err != nil {
		t.Fatalf("Record(first) error = %v", err)
	}
	second, err := recorder.Record(ctx, trace.Event{Type: trace.EventNodeStarted, RunID: "run_1", WorkflowID: "wf"})
	if err != nil {
		t.Fatalf("Record(second) error = %v", err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d", first.Sequence, second.Sequence)
	}

	events, err := recorder.List(ctx, "run_1", 1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("List(after=1) = %#v", events)
	}
}
