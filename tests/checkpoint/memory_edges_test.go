package checkpoint_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/superquail/langquail/checkpoint"
)

func TestMemoryStoreRejectsInvalidAndDuplicateCheckpoints(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemoryStore()
	tests := []struct {
		name       string
		checkpoint checkpoint.Checkpoint
		want       string
	}{
		{
			name:       "missing workflow",
			checkpoint: checkpoint.Checkpoint{RunID: "run_1", NodeID: "a", State: json.RawMessage(`{}`)},
			want:       "workflow id is required",
		},
		{
			name:       "missing run",
			checkpoint: checkpoint.Checkpoint{WorkflowID: "wf", NodeID: "a", State: json.RawMessage(`{}`)},
			want:       "run id is required",
		},
		{
			name:       "missing node",
			checkpoint: checkpoint.Checkpoint{WorkflowID: "wf", RunID: "run_1", State: json.RawMessage(`{}`)},
			want:       "node id is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Save(ctx, tt.checkpoint)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Save() error = %v, want %q", err, tt.want)
			}
		})
	}

	duplicate := checkpoint.Checkpoint{
		ID:         "chk_duplicate",
		WorkflowID: "wf",
		RunID:      "run_1",
		NodeID:     "a",
		State:      json.RawMessage(`{"value":"one"}`),
	}
	if _, err := store.Save(ctx, duplicate); err != nil {
		t.Fatalf("Save(first duplicate) error = %v", err)
	}
	if _, err := store.Save(ctx, duplicate); err == nil || !strings.Contains(err.Error(), "duplicate checkpoint id") {
		t.Fatalf("Save(second duplicate) error = %v, want duplicate", err)
	}
}

func TestMemoryStoreClonesStateOnSaveLoadAndList(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemoryStore()
	original := []byte(`{"value":"first"}`)
	saved, err := store.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: "wf",
		RunID:      "run_1",
		NodeID:     "a",
		State:      original,
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	original[0] = '['
	saved.State[0] = '['

	loaded, err := store.Load(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !bytes.Equal(loaded.State, []byte(`{"value":"first"}`)) {
		t.Fatalf("loaded.State = %s", loaded.State)
	}
	loaded.State[0] = '['

	loadedAgain, err := store.Load(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Load(again) error = %v", err)
	}
	if !bytes.Equal(loadedAgain.State, []byte(`{"value":"first"}`)) {
		t.Fatalf("loadedAgain.State = %s", loadedAgain.State)
	}

	list, err := store.List(ctx, "run_1")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	list[0].State[0] = '['
	loadedAfterListMutation, err := store.Load(ctx, saved.ID)
	if err != nil {
		t.Fatalf("Load(after list mutation) error = %v", err)
	}
	if !bytes.Equal(loadedAfterListMutation.State, []byte(`{"value":"first"}`)) {
		t.Fatalf("loadedAfterListMutation.State = %s", loadedAfterListMutation.State)
	}
}

func TestMemoryStoreRunIsolationLatestAndSerializerErrors(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemoryStore()
	runOne, err := store.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: "wf",
		RunID:      "run_1",
		NodeID:     "a",
		State:      json.RawMessage(`{"value":"one"}`),
	})
	if err != nil {
		t.Fatalf("Save(run_1) error = %v", err)
	}
	runTwo, err := store.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: "wf",
		RunID:      "run_2",
		NodeID:     "b",
		State:      json.RawMessage(`{"value":"two"}`),
	})
	if err != nil {
		t.Fatalf("Save(run_2) error = %v", err)
	}

	list, err := store.List(ctx, "run_1")
	if err != nil {
		t.Fatalf("List(run_1) error = %v", err)
	}
	if len(list) != 1 || list[0].ID != runOne.ID {
		t.Fatalf("List(run_1) = %#v", list)
	}
	latest, ok, err := store.Latest(ctx, "run_2")
	if err != nil {
		t.Fatalf("Latest(run_2) error = %v", err)
	}
	if !ok || latest.ID != runTwo.ID {
		t.Fatalf("Latest(run_2) = %#v, ok=%v", latest, ok)
	}
	empty, ok, err := store.Latest(ctx, "missing")
	if err != nil {
		t.Fatalf("Latest(missing) error = %v", err)
	}
	if ok || empty.ID != "" {
		t.Fatalf("Latest(missing) = %#v, ok=%v", empty, ok)
	}

	serializer := checkpoint.NewJSONSerializer[checkpointState]()
	if _, err := serializer.Unmarshal(json.RawMessage(`{`)); err == nil {
		t.Fatal("Unmarshal(invalid) error = nil, want decode error")
	}
}
