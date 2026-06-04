package trace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/superquail/langquail/trace"
)

func TestMemoryRecorderRejectsInvalidEvents(t *testing.T) {
	ctx := context.Background()
	recorder := trace.NewMemoryRecorder()
	tests := []struct {
		name  string
		event trace.Event
		want  string
	}{
		{
			name:  "missing run",
			event: trace.Event{Type: trace.EventRunStarted, WorkflowID: "wf"},
			want:  "run id is required",
		},
		{
			name:  "missing type",
			event: trace.Event{RunID: "run_1", WorkflowID: "wf"},
			want:  "event type is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := recorder.Record(ctx, tt.event)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Record() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestMemoryRecorderSequencesAndReplayAcrossRuns(t *testing.T) {
	ctx := context.Background()
	recorder := trace.NewMemoryRecorder()

	runOneFirst, err := recorder.Record(ctx, trace.Event{Type: trace.EventRunStarted, RunID: "run_1", WorkflowID: "wf"})
	if err != nil {
		t.Fatalf("Record(run_1 first) error = %v", err)
	}
	runTwoFirst, err := recorder.Record(ctx, trace.Event{Type: trace.EventRunStarted, RunID: "run_2", WorkflowID: "wf"})
	if err != nil {
		t.Fatalf("Record(run_2 first) error = %v", err)
	}
	runOneSecond, err := recorder.Record(ctx, trace.Event{Type: trace.EventNodeStarted, RunID: "run_1", WorkflowID: "wf"})
	if err != nil {
		t.Fatalf("Record(run_1 second) error = %v", err)
	}
	if runOneFirst.Sequence != 1 || runOneSecond.Sequence != 2 || runTwoFirst.Sequence != 1 {
		t.Fatalf("sequences = run_1:%d,%d run_2:%d", runOneFirst.Sequence, runOneSecond.Sequence, runTwoFirst.Sequence)
	}

	runOneReplay, err := recorder.List(ctx, "run_1", 1)
	if err != nil {
		t.Fatalf("List(run_1) error = %v", err)
	}
	if len(runOneReplay) != 1 || runOneReplay[0].Sequence != 2 || runOneReplay[0].RunID != "run_1" {
		t.Fatalf("List(run_1, after=1) = %#v", runOneReplay)
	}
	runTwoReplay, err := recorder.List(ctx, "run_2", 0)
	if err != nil {
		t.Fatalf("List(run_2) error = %v", err)
	}
	if len(runTwoReplay) != 1 || runTwoReplay[0].Sequence != 1 || runTwoReplay[0].RunID != "run_2" {
		t.Fatalf("List(run_2, after=0) = %#v", runTwoReplay)
	}
}

func TestMemoryRecorderClonesPayloadOnRecordAndList(t *testing.T) {
	ctx := context.Background()
	recorder := trace.NewMemoryRecorder()
	original := []byte(`{"value":"first"}`)
	recorded, err := recorder.Record(ctx, trace.Event{
		Type:       trace.EventRunStarted,
		RunID:      "run_payload",
		WorkflowID: "wf",
		Payload:    json.RawMessage(original),
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	original[0] = '['
	recorded.Payload[0] = '['

	events, err := recorder.List(ctx, "run_payload", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || !bytes.Equal(events[0].Payload, []byte(`{"value":"first"}`)) {
		t.Fatalf("List() payload = %#v", events)
	}
	events[0].Payload[0] = '['

	eventsAgain, err := recorder.List(ctx, "run_payload", 0)
	if err != nil {
		t.Fatalf("List(again) error = %v", err)
	}
	if len(eventsAgain) != 1 || !bytes.Equal(eventsAgain[0].Payload, []byte(`{"value":"first"}`)) {
		t.Fatalf("List(again) payload = %#v", eventsAgain)
	}
}
