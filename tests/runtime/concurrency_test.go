package runtime_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
)

func TestRunnerConcurrentInvoke(t *testing.T) {
	g := graph.NewStateGraph[runtimeState]("runtime.concurrent")
	g.Step("a", appendNode("a", 1))
	g.Step("b", appendNode("b", 1))
	g.Flow("a", "b")
	g.Start("a")
	g.Finish("b")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	const runs = 16
	errs := make(chan error, runs)
	var wg sync.WaitGroup
	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := runner.Invoke(context.Background(), runtimeState{}, lqruntime.WithRunID(fmt.Sprintf("run_%d", i)))
			if err != nil {
				errs <- err
				return
			}
			if result.Run.Status != lqruntime.StatusCompleted {
				errs <- fmt.Errorf("status = %s", result.Run.Status)
				return
			}
			if result.State.Count != 2 {
				errs <- fmt.Errorf("count = %d", result.State.Count)
				return
			}
			if len(result.Checkpoints) != 2 {
				errs <- fmt.Errorf("len(Checkpoints) = %d", len(result.Checkpoints))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
