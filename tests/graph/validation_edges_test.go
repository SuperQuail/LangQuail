package graph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
)

func TestValidateRejectsMissingWorkflowStartAndUnknownFinish(t *testing.T) {
	tests := []struct {
		name  string
		build func() *graph.StateGraph[graphState]
		want  string
	}{
		{
			name: "empty workflow",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("")
				g.Step("a", noopNode)
				g.Start("a")
				return g
			},
			want: "workflow id",
		},
		{
			name: "missing start",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.missing.start")
				g.Step("a", noopNode)
				return g
			},
			want: "start node is required",
		},
		{
			name: "unknown start",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.unknown.start")
				g.Step("a", noopNode)
				g.Start("missing")
				return g
			},
			want: "start node",
		},
		{
			name: "unknown finish",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.unknown.finish")
				g.Step("a", noopNode)
				g.Start("a")
				g.Finish("missing")
				return g
			},
			want: "finish node",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build().Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsFixedAndRouteFromSameNode(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.fixed.and.route")
	g.Step("a", noopNode)
	g.Step("b", noopNode)
	g.Step("c", noopNode)
	g.Edge("a", "b")
	g.Route("a").Otherwise("c")
	g.Start("a")

	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "both fixed edges and a route") {
		t.Fatalf("Validate() error = %v, want fixed and route conflict", err)
	}
}

func TestValidateRejectsDuplicateRoute(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.duplicate.route")
	g.Step("a", noopNode)
	g.Step("b", noopNode)
	g.Route("a").Otherwise("b")
	g.Route("a").Otherwise("b")
	g.Start("a")

	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("Validate() error = %v, want duplicate route", err)
	}
}

func TestValidateRejectsNilPredicateAndEmptyTargets(t *testing.T) {
	tests := []struct {
		name  string
		build func() *graph.StateGraph[graphState]
		want  string
	}{
		{
			name: "nil predicate",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.nil.predicate")
				g.Step("a", noopNode)
				g.Step("b", noopNode)
				g.Route("a").When(nil, "b")
				g.Start("a")
				return g
			},
			want: "nil predicate",
		},
		{
			name: "empty when target",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.empty.when")
				g.Step("a", noopNode)
				g.Route("a").When(func(ctx context.Context, state graphState) (bool, error) {
					return true, nil
				}, "")
				g.Start("a")
				return g
			},
			want: "empty target",
		},
		{
			name: "empty otherwise target",
			build: func() *graph.StateGraph[graphState] {
				g := graph.NewStateGraph[graphState]("test.empty.otherwise")
				g.Step("a", noopNode)
				g.Route("a").Otherwise("")
				g.Start("a")
				return g
			},
			want: "empty otherwise target",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.build().Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSelectRoutePropagatesPredicateError(t *testing.T) {
	wantErr := errors.New("predicate failed")
	g := graph.NewStateGraph[graphState]("test.route.error")
	g.Step("check", noopNode)
	g.Step("done", noopNode)
	g.Route("check").When(func(ctx context.Context, state graphState) (bool, error) {
		return false, wantErr
	}, "done")
	g.Start("check")
	g.Finish("done")

	if err := g.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	_, selected, err := g.SelectRoute(context.Background(), "check", graphState{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SelectRoute() error = %v, want %v", err, wantErr)
	}
	if selected {
		t.Fatal("SelectRoute() selected route despite predicate error")
	}
}
