package graph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
)

type graphState struct {
	Count int
}

func noopNode(ctx context.Context, state graphState) (graph.Command[graphState], error) {
	return graph.Noop[graphState](), nil
}

func TestValidateAndSnapshot(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.graph")
	g.Step("start", noopNode)
	g.Step("middle", noopNode)
	g.Step("done", noopNode)
	g.Flow("start", "middle")
	g.Route("middle").
		When(func(ctx context.Context, state graphState) (bool, error) {
			return state.Count > 0, nil
		}, "done").
		Otherwise("start")
	g.Start("start")
	g.Finish("done")

	if err := g.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	snapshot := g.Snapshot()
	if snapshot.WorkflowID != "test.graph" {
		t.Fatalf("WorkflowID = %q", snapshot.WorkflowID)
	}
	if snapshot.Start != "start" {
		t.Fatalf("Start = %q", snapshot.Start)
	}
	if len(snapshot.Nodes) != 3 {
		t.Fatalf("len(Nodes) = %d", len(snapshot.Nodes))
	}
	if len(snapshot.Edges) != 3 {
		t.Fatalf("len(Edges) = %d", len(snapshot.Edges))
	}
	if snapshot.Edges[0].From != "start" || snapshot.Edges[0].To != "middle" {
		t.Fatalf("first edge = %#v", snapshot.Edges[0])
	}
}

func TestValidateDuplicateNode(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.duplicate")
	g.Step("a", noopNode)
	g.Step("a", noopNode)
	g.Start("a")
	g.Finish("a")

	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate node") {
		t.Fatalf("Validate() error = %v, want duplicate node", err)
	}
}

func TestValidateUnknownEdgeTarget(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.unknown")
	g.Step("a", noopNode)
	g.Edge("a", "missing")
	g.Start("a")

	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Validate() error = %v, want missing target", err)
	}
}

func TestSelectRoute(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.route")
	g.Step("check", noopNode)
	g.Step("empty", noopNode)
	g.Step("strict", noopNode)
	g.Step("low", noopNode)
	g.Step("high", noopNode)
	g.Route("check").
		When(func(ctx context.Context, state graphState) (bool, error) {
			return state.Count > 10, nil
		}, "high").
		Otherwise("low")
	g.Route("strict").
		When(func(ctx context.Context, state graphState) (bool, error) {
			return state.Count > 10, nil
		}, "high")
	g.Start("check")
	g.Finish("empty", "low", "high")

	if err := g.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name     string
		from     string
		state    graphState
		selected bool
		want     graph.RouteSelection
	}{
		{
			name:     "otherwise",
			from:     "check",
			state:    graphState{Count: 2},
			selected: true,
			want: graph.RouteSelection{
				Target:  "low",
				Kind:    graph.EdgeKindOtherwise,
				Default: true,
			},
		},
		{
			name:     "conditional",
			from:     "check",
			state:    graphState{Count: 11},
			selected: true,
			want: graph.RouteSelection{
				Target: "high",
				Kind:   graph.EdgeKindConditional,
				Order:  1,
			},
		},
		{
			name:     "no route",
			from:     "empty",
			state:    graphState{Count: 11},
			selected: false,
		},
		{
			name:     "no matching route",
			from:     "strict",
			state:    graphState{Count: 2},
			selected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selection, selected, err := g.SelectRoute(context.Background(), tt.from, tt.state)
			if err != nil {
				t.Fatalf("SelectRoute() error = %v", err)
			}
			if selected != tt.selected || selection != tt.want {
				t.Fatalf("SelectRoute() = %#v, selected=%v", selection, selected)
			}
		})
	}
}
