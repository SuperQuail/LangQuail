package benchmarks_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

type benchState struct {
	Count int
}

func BenchmarkRuntimeLinearFlow(b *testing.B) {
	g := graph.NewStateGraph[benchState]("bench.runtime.linear")
	g.Step("a", benchAppend(1))
	g.Step("b", benchAppend(1))
	g.Flow("a", "b")
	g.Start("a")
	g.Finish("b")
	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runner.Invoke(ctx, benchState{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelectRoute(b *testing.B) {
	g := graph.NewStateGraph[benchState]("bench.route")
	g.Step("check", benchAppend(0))
	g.Step("low", benchAppend(0))
	g.Step("high", benchAppend(0))
	g.Route("check").
		When(func(ctx context.Context, state benchState) (bool, error) {
			return state.Count > 10, nil
		}, "high").
		Otherwise("low")
	g.Start("check")
	g.Finish("low", "high")
	if err := g.Validate(); err != nil {
		b.Fatal(err)
	}

	ctx := context.Background()
	state := benchState{Count: 11}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, selected, err := g.SelectRoute(ctx, "check", state); err != nil || !selected {
			b.Fatalf("SelectRoute() selected=%v err=%v", selected, err)
		}
	}
}

func BenchmarkMemoryRecorderRecord(b *testing.B) {
	recorder := trace.NewMemoryRecorder()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := recorder.Record(ctx, trace.Event{
			Type:       "bench.event",
			WorkflowID: "workflow",
			RunID:      "run_record",
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryRecorderList(b *testing.B) {
	recorder := trace.NewMemoryRecorder()
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		if _, err := recorder.Record(ctx, trace.Event{
			Type:       "bench.event",
			WorkflowID: "workflow",
			RunID:      "run_list",
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := recorder.List(ctx, "run_list", 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryStoreSave(b *testing.B) {
	store := checkpoint.NewMemoryStore()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.Save(ctx, checkpoint.Checkpoint{
			WorkflowID: "workflow",
			RunID:      "run_save",
			NodeID:     "node",
			Sequence:   int64(i + 1),
			State:      []byte(`{"count":1}`),
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMemoryStoreList(b *testing.B) {
	store := checkpoint.NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		if _, err := store.Save(ctx, checkpoint.Checkpoint{
			WorkflowID: "workflow",
			RunID:      "run_list",
			NodeID:     "node",
			Sequence:   int64(i + 1),
			State:      []byte(fmt.Sprintf(`{"count":%d}`, i)),
		}); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.List(ctx, "run_list"); err != nil {
			b.Fatal(err)
		}
	}
}

func benchAppend(delta int) graph.NodeFunc[benchState] {
	return func(ctx context.Context, state benchState) (graph.Command[benchState], error) {
		state.Count += delta
		return graph.Update(state), nil
	}
}
