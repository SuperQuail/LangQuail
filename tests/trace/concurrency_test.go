package trace_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/superquail/langquail/trace"
)

func TestMemoryRecorderConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	recorder := trace.NewMemoryRecorder()
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
				_, err := recorder.Record(ctx, trace.Event{
					Type:       "test.event",
					WorkflowID: "workflow",
					RunID:      "run_concurrent",
					NodeID:     fmt.Sprintf("node_%d", worker),
				})
				if err != nil {
					errs <- err
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

	events, err := recorder.List(ctx, "run_concurrent", 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != total {
		t.Fatalf("len(List()) = %d, want %d", len(events), total)
	}
	seen := make(map[int64]struct{}, total)
	for _, event := range events {
		if event.Sequence < 1 || event.Sequence > total {
			t.Fatalf("sequence out of range: %d", event.Sequence)
		}
		if _, exists := seen[event.Sequence]; exists {
			t.Fatalf("duplicate sequence %d", event.Sequence)
		}
		seen[event.Sequence] = struct{}{}
	}
}
