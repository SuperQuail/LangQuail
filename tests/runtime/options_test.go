package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

func TestInvokeOptionsSetRunSessionMetadataAndStartAt(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.options")
	g.Step("a", appendNode("a", 1))
	g.Step("b", appendNode("b", 1))
	g.Step("c", appendNode("c", 1))
	g.Flow("a", "b", "c")
	g.Start("a")
	g.Finish("c")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{},
		lqruntime.WithRunID("run_custom"),
		lqruntime.WithSession("session_1"),
		lqruntime.WithMetadata(map[string]string{"tenant": "acme"}),
		lqruntime.WithStartAt("b"),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.ID != "run_custom" || result.Run.SessionID != "session_1" {
		t.Fatalf("run = %#v", result.Run)
	}
	if result.Run.Metadata["tenant"] != "acme" {
		t.Fatalf("metadata = %#v", result.Run.Metadata)
	}
	if strings.Join(result.State.Path, ",") != "b,c" {
		t.Fatalf("Path = %#v", result.State.Path)
	}
}

func TestInvokeWithUnknownStartAtReturnsError(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.options.missing")
	g.Step("a", appendNode("a", 1))
	g.Start("a")
	g.Finish("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{}, lqruntime.WithStartAt("missing"))
	if err == nil || !strings.Contains(err.Error(), `start node "missing"`) {
		t.Fatalf("Invoke() error = %v, want missing start", err)
	}
	if result != nil {
		t.Fatalf("Invoke() result = %#v, want nil", result)
	}
}

func TestRunnerUsesCustomRecorderCheckpointStoreAndSerializer(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.custom.options")
	g.Step("a", appendNode("a", 1))
	g.Start("a")
	g.Finish("a")

	recorder := &countingRecorder{inner: trace.NewMemoryRecorder()}
	store := &countingCheckpointStore{inner: checkpoint.NewMemoryStore()}
	serializer := &countingRuntimeSerializer{inner: checkpoint.NewJSONSerializer[runtimeState]()}
	runner, err := lqruntime.NewRunner(g,
		lqruntime.WithRecorder[runtimeState](recorder),
		lqruntime.WithCheckpointStore[runtimeState](store),
		lqruntime.WithSerializer[runtimeState](serializer),
	)
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
	if recorder.records == 0 || recorder.lists == 0 {
		t.Fatalf("recorder counts = records:%d lists:%d", recorder.records, recorder.lists)
	}
	if store.saves == 0 || store.lists == 0 {
		t.Fatalf("store counts = saves:%d lists:%d", store.saves, store.lists)
	}
	if serializer.marshals == 0 {
		t.Fatalf("serializer marshals = %d", serializer.marshals)
	}
}

func TestRuntimeFailsOnMultipleFixedEdges(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.multiple.fixed")
	g.Step("a", appendNode("a", 1))
	g.Step("b", appendNode("b", 1))
	g.Step("c", appendNode("c", 1))
	g.Edge("a", "b")
	g.Edge("a", "c")
	g.Start("a")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), runtimeState{})
	if err == nil || !strings.Contains(err.Error(), "multiple fixed edges") {
		t.Fatalf("Invoke() error = %v, want multiple fixed edges", err)
	}
	if result == nil || result.Run.Status != lqruntime.StatusFailed {
		t.Fatalf("result = %#v", result)
	}
}

type countingRecorder struct {
	inner   trace.Recorder
	records int
	lists   int
}

func (r *countingRecorder) Record(ctx context.Context, event trace.Event) (trace.Event, error) {
	r.records++
	return r.inner.Record(ctx, event)
}

func (r *countingRecorder) List(ctx context.Context, runID string, afterSequence int64) ([]trace.Event, error) {
	r.lists++
	return r.inner.List(ctx, runID, afterSequence)
}

type countingCheckpointStore struct {
	inner checkpoint.Store
	saves int
	lists int
}

func (s *countingCheckpointStore) Save(ctx context.Context, item checkpoint.Checkpoint) (checkpoint.Checkpoint, error) {
	s.saves++
	return s.inner.Save(ctx, item)
}

func (s *countingCheckpointStore) Load(ctx context.Context, id string) (checkpoint.Checkpoint, error) {
	return s.inner.Load(ctx, id)
}

func (s *countingCheckpointStore) List(ctx context.Context, runID string) ([]checkpoint.Checkpoint, error) {
	s.lists++
	return s.inner.List(ctx, runID)
}

func (s *countingCheckpointStore) Latest(ctx context.Context, runID string) (checkpoint.Checkpoint, bool, error) {
	return s.inner.Latest(ctx, runID)
}

type countingRuntimeSerializer struct {
	inner      checkpoint.JSONSerializer[runtimeState]
	marshals   int
	unmarshals int
}

func (s *countingRuntimeSerializer) Marshal(state runtimeState) (json.RawMessage, error) {
	s.marshals++
	return s.inner.Marshal(state)
}

func (s *countingRuntimeSerializer) Unmarshal(snapshot json.RawMessage) (runtimeState, error) {
	s.unmarshals++
	return s.inner.Unmarshal(snapshot)
}
