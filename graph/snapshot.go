package graph

type Snapshot struct {
	WorkflowID string         `json:"workflow_id"`
	Start      string         `json:"start"`
	Finish     []string       `json:"finish"`
	Nodes      []NodeSnapshot `json:"nodes"`
	Edges      []EdgeSnapshot `json:"edges"`
}

type NodeSnapshot struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type EdgeSnapshot struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Kind    string `json:"kind"`
	Label   string `json:"label,omitempty"`
	Order   int    `json:"order,omitempty"`
	Default bool   `json:"default,omitempty"`
}

func (g *StateGraph[S]) Snapshot() Snapshot {
	if g == nil {
		return Snapshot{}
	}

	snapshot := Snapshot{
		WorkflowID: g.workflowID,
		Start:      g.start,
		Finish:     append([]string(nil), g.finishList...),
		Nodes:      make([]NodeSnapshot, 0, len(g.nodeOrder)),
	}
	for _, id := range g.nodeOrder {
		node := g.nodes[id]
		snapshot.Nodes = append(snapshot.Nodes, NodeSnapshot{
			ID:       node.id,
			Kind:     string(node.kind),
			Metadata: cloneMetadata(node.metadata),
		})
	}
	for index, edge := range g.fixedOrder {
		snapshot.Edges = append(snapshot.Edges, EdgeSnapshot{
			From:  edge.from,
			To:    edge.to,
			Kind:  string(EdgeKindFixed),
			Order: index + 1,
		})
	}
	for _, from := range g.nodeOrder {
		route, exists := g.routes[from]
		if !exists {
			continue
		}
		for _, edge := range route.whens {
			snapshot.Edges = append(snapshot.Edges, EdgeSnapshot{
				From:  from,
				To:    edge.target,
				Kind:  string(EdgeKindConditional),
				Label: "when",
				Order: edge.order,
			})
		}
		if route.otherwise != "" {
			snapshot.Edges = append(snapshot.Edges, EdgeSnapshot{
				From:    from,
				To:      route.otherwise,
				Kind:    string(EdgeKindOtherwise),
				Label:   "otherwise",
				Default: true,
			})
		}
	}
	return snapshot
}
