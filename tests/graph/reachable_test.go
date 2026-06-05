package graph_test

import (
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
)

func TestReachableAddsDynamicSnapshotEdge(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.reachable")
	g.Step("start", noopNode)
	g.Step("internal", noopNode)
	g.Step("done", noopNode)
	g.Edge("start", "done")
	g.Reachable("start", "internal", graph.EdgeLabel("manual"), graph.EdgeDescription("internal goto"))
	g.Start("start")
	g.Finish("done")

	if err := g.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	snapshot := g.Snapshot()
	var dynamic graph.EdgeSnapshot
	for _, edge := range snapshot.Edges {
		if edge.Kind == string(graph.EdgeKindDynamic) {
			dynamic = edge
			break
		}
	}
	if dynamic.From != "start" || dynamic.To != "internal" || dynamic.Label != "manual" || dynamic.Description != "internal goto" {
		t.Fatalf("dynamic edge = %#v", dynamic)
	}
}

func TestValidateRejectsUnknownReachableEndpoint(t *testing.T) {
	g := graph.NewStateGraph[graphState]("test.bad.reachable")
	g.Step("start", noopNode)
	g.Reachable("start", "missing")
	g.Start("start")
	g.Finish("start")

	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "reachable target") {
		t.Fatalf("Validate() error = %v, want reachable target", err)
	}
}
