package graph

type NodeSpec[S any] struct {
	ID       string
	Kind     NodeKind
	Run      NodeFunc[S]
	Metadata map[string]string
}

func (g *StateGraph[S]) Node(spec NodeSpec[S]) *StateGraph[S] {
	if g == nil {
		return g
	}
	if spec.ID == "" {
		g.addError("graph: node id cannot be empty")
		return g
	}
	if spec.Run == nil {
		g.addError("graph: node %q has nil node function", spec.ID)
		return g
	}
	if spec.Kind == "" {
		spec.Kind = NodeKindStep
	}
	if _, exists := g.nodes[spec.ID]; exists {
		g.addError("graph: duplicate node %q", spec.ID)
		return g
	}
	g.nodes[spec.ID] = node[S]{
		id:       spec.ID,
		kind:     spec.Kind,
		fn:       spec.Run,
		metadata: cloneMetadata(spec.Metadata),
	}
	g.nodeOrder = append(g.nodeOrder, spec.ID)
	return g
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	clone := make(map[string]string, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}
