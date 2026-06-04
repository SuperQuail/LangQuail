package checkpoint_test

import (
	"context"
	"testing"

	"github.com/superquail/langquail/checkpoint"
)

type checkpointState struct {
	Value string
}

func TestMemoryStoreSaveLoadLatest(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemoryStore()
	serializer := checkpoint.NewJSONSerializer[checkpointState]()

	firstState, err := serializer.Marshal(checkpointState{Value: "first"})
	if err != nil {
		t.Fatalf("Marshal(first) error = %v", err)
	}
	first, err := store.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: "wf",
		RunID:      "run_1",
		NodeID:     "a",
		Sequence:   1,
		State:      firstState,
	})
	if err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}

	secondState, err := serializer.Marshal(checkpointState{Value: "second"})
	if err != nil {
		t.Fatalf("Marshal(second) error = %v", err)
	}
	second, err := store.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: "wf",
		RunID:      "run_1",
		NodeID:     "b",
		Sequence:   2,
		State:      secondState,
	})
	if err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}

	loaded, err := store.Load(ctx, first.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var decoded checkpointState
	if decoded, err = serializer.Unmarshal(loaded.State); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Value != "first" {
		t.Fatalf("decoded.Value = %q", decoded.Value)
	}

	latest, ok, err := store.Latest(ctx, "run_1")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !ok || latest.ID != second.ID {
		t.Fatalf("Latest() = %#v, ok=%v", latest, ok)
	}

	list, err := store.List(ctx, "run_1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("List() = %#v", list)
	}
}
