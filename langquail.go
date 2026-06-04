package langquail

import (
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
)

func NewStateGraph[S any](workflowID string) *graph.StateGraph[S] {
	return graph.NewStateGraph[S](workflowID)
}

func Update[S any](state S) graph.Command[S] {
	return graph.Update(state)
}

func Goto[S any](target string) graph.Command[S] {
	return graph.Goto[S](target)
}

func End[S any]() graph.Command[S] {
	return graph.End[S]()
}

func Noop[S any]() graph.Command[S] {
	return graph.Noop[S]()
}

func NewRunner[S any](stateGraph *graph.StateGraph[S], opts ...runtime.Option[S]) (*runtime.Runner[S], error) {
	return runtime.NewRunner(stateGraph, opts...)
}

func NewToolRegistry() *tool.Registry {
	return tool.NewRegistry()
}
