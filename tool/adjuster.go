package tool

import (
	"bytes"
	"context"
	"encoding/json"
)

type Adjuster interface {
	AfterTool(context.Context, AfterToolRequest) (AfterToolResult, error)
}

type AfterToolRequest struct {
	NodeID   string            `json:"node_id"`
	Call     Call              `json:"call"`
	Result   Result            `json:"result"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type AfterToolResult struct {
	Result *Result `json:"result,omitempty"`
}

type adjusterContextKey struct{}

func WithAdjuster(ctx context.Context, adjuster Adjuster) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if adjuster == nil {
		return ctx
	}
	return context.WithValue(ctx, adjusterContextKey{}, adjuster)
}

func AdjusterFromContext(ctx context.Context) (Adjuster, bool) {
	if ctx == nil {
		return nil, false
	}
	adjuster, ok := ctx.Value(adjusterContextKey{}).(Adjuster)
	return adjuster, ok && adjuster != nil
}

func cloneCall(call Call) Call {
	call.Arguments = json.RawMessage(bytes.Clone(call.Arguments))
	return call
}

func cloneResult(result Result) Result {
	result.Raw = json.RawMessage(bytes.Clone(result.Raw))
	return result
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
