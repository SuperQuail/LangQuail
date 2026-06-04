package checkpoint_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/superquail/langquail/checkpoint"
)

func TestMemoryStoreConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemoryStore()
	const workers = 16
	const perWorker = 20
	const total = workers * perWorker

	errs := make(chan error, total)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				state := []byte(fmt.Sprintf(`{"worker":%d,"item":%d}`, worker, i))
				saved, err := store.Save(ctx, checkpoint.Checkpoint{
					WorkflowID: "workflow",
					RunID:      "run_concurrent",
					NodeID:     "node",
					Sequence:   int64(worker*perWorker + i + 1),
					State:      state,
				})
				if err != nil {
					errs <- err
					continue
				}
				state[0] = 'X'
				loaded, err := store.Load(ctx, saved.ID)
				if err != nil {
					errs <- err
					continue
				}
				if len(loaded.State) == 0 || loaded.State[0] == 'X' {
					errs <- fmt.Errorf("loaded state was externally mutated: %s", loaded.State)
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	list, err := store.List(ctx, "run_concurrent")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != total {
		t.Fatalf("len(List()) = %d, want %d", len(list), total)
	}
	ids := make(map[string]struct{}, len(list))
	for _, item := range list {
		if item.ID == "" {
			t.Fatal("checkpoint ID is empty")
		}
		if _, exists := ids[item.ID]; exists {
			t.Fatalf("duplicate checkpoint ID %q", item.ID)
		}
		ids[item.ID] = struct{}{}
	}
	latest, ok, err := store.Latest(ctx, "run_concurrent")
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	if !ok || latest.RunID != "run_concurrent" {
		t.Fatalf("Latest() = %#v, ok=%v", latest, ok)
	}
}
