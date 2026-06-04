package hitl

import (
	"context"
	"errors"

	"github.com/superquail/langquail/graph"
)

type responseContextKey struct{}

func WithResponse(ctx context.Context, response Response) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, responseContextKey{}, response)
}

func ResponseFromContext(ctx context.Context) (Response, bool) {
	if ctx == nil {
		return Response{}, false
	}
	response, ok := ctx.Value(responseContextKey{}).(Response)
	return response, ok
}

type NodeSpec[S any] struct {
	Request  func(context.Context, S) (Request, error)
	Output   func(context.Context, S, Response) (graph.Command[S], error)
	Metadata map[string]string
}

func Node[S any](id string, spec NodeSpec[S]) graph.NodeSpec[S] {
	metadata := map[string]string{"node": "human"}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	return graph.NodeSpec[S]{
		ID:       id,
		Kind:     graph.NodeKindHuman,
		Metadata: metadata,
		Run: func(ctx context.Context, state S) (graph.Command[S], error) {
			if response, ok := ResponseFromContext(ctx); ok {
				if spec.Output == nil {
					return graph.Noop[S](), nil
				}
				return spec.Output(ctx, state, response)
			}
			if spec.Request == nil {
				return graph.Noop[S](), errors.New("hitl: human node request builder is required")
			}
			request, err := spec.Request(ctx, state)
			if err != nil {
				return graph.Noop[S](), err
			}
			return graph.Command[S]{
				Interrupt: &graph.Interrupt{
					Kind:    string(request.Kind),
					Reason:  request.Reason,
					Payload: request,
				},
			}, nil
		},
	}
}
