package graph

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

type NodeFunc[S any] func(context.Context, S) (Command[S], error)

type Predicate[S any] func(context.Context, S) (bool, error)

type NodeKind string

const (
	NodeKindStep  NodeKind = "step"
	NodeKindLLM   NodeKind = "llm"
	NodeKindTool  NodeKind = "tool"
	NodeKindHuman NodeKind = "human"
)

type EdgeKind string

const (
	EdgeKindFixed       EdgeKind = "fixed"
	EdgeKindConditional EdgeKind = "conditional"
	EdgeKindOtherwise   EdgeKind = "otherwise"
	EdgeKindDynamic     EdgeKind = "dynamic"
)

type node[S any] struct {
	id       string
	kind     NodeKind
	fn       NodeFunc[S]
	metadata map[string]string
}

type conditionalEdge[S any] struct {
	target    string
	predicate Predicate[S]
	order     int
}

type route[S any] struct {
	from      string
	whens     []conditionalEdge[S]
	otherwise string
}

type fixedEdge struct {
	from string
	to   string
}

type StateGraph[S any] struct {
	workflowID string
	nodes      map[string]node[S]
	nodeOrder  []string
	fixed      map[string][]string
	fixedOrder []fixedEdge
	routes     map[string]*route[S]
	start      string
	finish     map[string]struct{}
	finishList []string
	buildErrs  []error
}

type RouteSelection struct {
	Target  string
	Kind    EdgeKind
	Order   int
	Default bool
}

func NewStateGraph[S any](workflowID string) *StateGraph[S] {
	return &StateGraph[S]{
		workflowID: workflowID,
		nodes:      make(map[string]node[S]),
		fixed:      make(map[string][]string),
		routes:     make(map[string]*route[S]),
		finish:     make(map[string]struct{}),
	}
}

func (g *StateGraph[S]) WorkflowID() string {
	if g == nil {
		return ""
	}
	return g.workflowID
}

func (g *StateGraph[S]) Step(id string, fn NodeFunc[S]) *StateGraph[S] {
	return g.Node(NodeSpec[S]{
		ID:   id,
		Kind: NodeKindStep,
		Run:  fn,
	})
}

func (g *StateGraph[S]) Edge(from string, to string) *StateGraph[S] {
	if g == nil {
		return g
	}
	if from == "" || to == "" {
		g.addError("graph: edge endpoints cannot be empty")
		return g
	}
	g.fixed[from] = append(g.fixed[from], to)
	g.fixedOrder = append(g.fixedOrder, fixedEdge{from: from, to: to})
	return g
}

func (g *StateGraph[S]) Flow(ids ...string) *StateGraph[S] {
	for i := 0; i+1 < len(ids); i++ {
		g.Edge(ids[i], ids[i+1])
	}
	return g
}

func (g *StateGraph[S]) Route(from string) *RouteBuilder[S] {
	if g == nil {
		return &RouteBuilder[S]{graph: g}
	}
	if from == "" {
		g.addError("graph: route source cannot be empty")
		return &RouteBuilder[S]{graph: g}
	}
	if _, exists := g.routes[from]; exists {
		g.addError("graph: duplicate route from %q", from)
		return &RouteBuilder[S]{graph: g, route: g.routes[from]}
	}
	r := &route[S]{from: from}
	g.routes[from] = r
	return &RouteBuilder[S]{graph: g, route: r}
}

func (g *StateGraph[S]) Start(id string) *StateGraph[S] {
	if g == nil {
		return g
	}
	if id == "" {
		g.addError("graph: start cannot be empty")
		return g
	}
	g.start = id
	return g
}

func (g *StateGraph[S]) Finish(ids ...string) *StateGraph[S] {
	if g == nil {
		return g
	}
	for _, id := range ids {
		if id == "" {
			g.addError("graph: finish node cannot be empty")
			continue
		}
		if _, exists := g.finish[id]; !exists {
			g.finish[id] = struct{}{}
			g.finishList = append(g.finishList, id)
		}
	}
	return g
}

func (g *StateGraph[S]) Validate() error {
	if g == nil {
		return errors.New("graph: nil StateGraph")
	}

	errs := slices.Clone(g.buildErrs)
	if g.workflowID == "" {
		errs = append(errs, errors.New("graph: workflow id cannot be empty"))
	}
	if len(g.nodes) == 0 {
		errs = append(errs, errors.New("graph: at least one node is required"))
	}
	if g.start == "" {
		errs = append(errs, errors.New("graph: start node is required"))
	} else if !g.HasNode(g.start) {
		errs = append(errs, fmt.Errorf("graph: start node %q is not registered", g.start))
	}
	for _, id := range g.finishList {
		if !g.HasNode(id) {
			errs = append(errs, fmt.Errorf("graph: finish node %q is not registered", id))
		}
	}
	for from, targets := range g.fixed {
		if !g.HasNode(from) {
			errs = append(errs, fmt.Errorf("graph: edge source %q is not registered", from))
		}
		if _, hasRoute := g.routes[from]; hasRoute {
			errs = append(errs, fmt.Errorf("graph: node %q has both fixed edges and a route", from))
		}
		for _, target := range targets {
			if !g.HasNode(target) {
				errs = append(errs, fmt.Errorf("graph: edge target %q from %q is not registered", target, from))
			}
		}
	}
	for from, route := range g.routes {
		if !g.HasNode(from) {
			errs = append(errs, fmt.Errorf("graph: route source %q is not registered", from))
		}
		for _, edge := range route.whens {
			if edge.predicate == nil {
				errs = append(errs, fmt.Errorf("graph: route from %q has nil predicate", from))
			}
			if !g.HasNode(edge.target) {
				errs = append(errs, fmt.Errorf("graph: route target %q from %q is not registered", edge.target, from))
			}
		}
		if route.otherwise != "" && !g.HasNode(route.otherwise) {
			errs = append(errs, fmt.Errorf("graph: otherwise target %q from %q is not registered", route.otherwise, from))
		}
	}
	return errors.Join(errs...)
}

func (g *StateGraph[S]) HasNode(id string) bool {
	if g == nil {
		return false
	}
	_, exists := g.nodes[id]
	return exists
}

func (g *StateGraph[S]) StartNode() string {
	if g == nil {
		return ""
	}
	return g.start
}

func (g *StateGraph[S]) IsFinish(id string) bool {
	if g == nil {
		return false
	}
	_, exists := g.finish[id]
	return exists
}

func (g *StateGraph[S]) NodeFunc(id string) (NodeFunc[S], bool) {
	if g == nil {
		return nil, false
	}
	node, exists := g.nodes[id]
	if !exists {
		return nil, false
	}
	return node.fn, true
}

func (g *StateGraph[S]) FixedTargets(id string) []string {
	if g == nil {
		return nil
	}
	return slices.Clone(g.fixed[id])
}

func (g *StateGraph[S]) SelectRoute(ctx context.Context, from string, state S) (RouteSelection, bool, error) {
	if g == nil {
		return RouteSelection{}, false, errors.New("graph: nil StateGraph")
	}
	route, exists := g.routes[from]
	if !exists {
		return RouteSelection{}, false, nil
	}
	for _, edge := range route.whens {
		ok, err := edge.predicate(ctx, state)
		if err != nil {
			return RouteSelection{}, false, err
		}
		if ok {
			return RouteSelection{
				Target: edge.target,
				Kind:   EdgeKindConditional,
				Order:  edge.order,
			}, true, nil
		}
	}
	if route.otherwise != "" {
		return RouteSelection{
			Target:  route.otherwise,
			Kind:    EdgeKindOtherwise,
			Default: true,
		}, true, nil
	}
	return RouteSelection{}, false, nil
}

func (g *StateGraph[S]) addError(format string, args ...any) {
	g.buildErrs = append(g.buildErrs, fmt.Errorf(format, args...))
}

type RouteBuilder[S any] struct {
	graph *StateGraph[S]
	route *route[S]
}

func (b *RouteBuilder[S]) When(predicate Predicate[S], target string) *RouteBuilder[S] {
	if b == nil || b.graph == nil {
		return b
	}
	if b.route == nil {
		b.graph.addError("graph: route builder is not attached")
		return b
	}
	if predicate == nil {
		b.graph.addError("graph: route from %q has nil predicate", b.route.from)
		return b
	}
	if target == "" {
		b.graph.addError("graph: route from %q has empty target", b.route.from)
		return b
	}
	b.route.whens = append(b.route.whens, conditionalEdge[S]{
		target:    target,
		predicate: predicate,
		order:     len(b.route.whens) + 1,
	})
	return b
}

func (b *RouteBuilder[S]) Otherwise(target string) *StateGraph[S] {
	if b == nil {
		return nil
	}
	if b.graph == nil {
		return nil
	}
	if b.route == nil {
		b.graph.addError("graph: route builder is not attached")
		return b.graph
	}
	if target == "" {
		b.graph.addError("graph: route from %q has empty otherwise target", b.route.from)
		return b.graph
	}
	b.route.otherwise = target
	return b.graph
}
