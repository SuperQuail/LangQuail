package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	"github.com/superquail/langquail/runtime"
)

type ExecutableWorkflow interface {
	graph.Workflow
	InvokeJSON(context.Context, json.RawMessage, ...runtime.InvokeOption) (json.RawMessage, error)
	ResumeJSON(context.Context, ResumeJSONRequest) (json.RawMessage, error)
}

type ResumeJSONRequest struct {
	Run          runtime.Run     `json:"run"`
	State        json.RawMessage `json:"state"`
	ResumeNode   string          `json:"resume_node"`
	Response     hitl.Response   `json:"response"`
	InterruptID  string          `json:"interrupt_id,omitempty"`
	CheckpointID string          `json:"checkpoint_id,omitempty"`
}

type executableWorkflow[S any] struct {
	graph   *graph.StateGraph[S]
	options []runtime.Option[S]
}

func Executable[S any](stateGraph *graph.StateGraph[S], opts ...runtime.Option[S]) ExecutableWorkflow {
	return &executableWorkflow[S]{
		graph:   stateGraph,
		options: append([]runtime.Option[S](nil), opts...),
	}
}

func (w *executableWorkflow[S]) WorkflowID() string {
	if w == nil || w.graph == nil {
		return ""
	}
	return w.graph.WorkflowID()
}

func (w *executableWorkflow[S]) Snapshot() graph.Snapshot {
	if w == nil || w.graph == nil {
		return graph.Snapshot{}
	}
	return w.graph.Snapshot()
}

func (w *executableWorkflow[S]) Validate() error {
	if w == nil || w.graph == nil {
		return errors.New("api: executable workflow graph is required")
	}
	return w.graph.Validate()
}

func (w *executableWorkflow[S]) InvokeJSON(ctx context.Context, input json.RawMessage, opts ...runtime.InvokeOption) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var state S
	if len(bytes.TrimSpace(input)) > 0 {
		if err := json.Unmarshal(input, &state); err != nil {
			return nil, err
		}
	}
	runnerOptions := w.runnerOptions(ctx)
	runner, err := runtime.NewRunner(w.graph, runnerOptions...)
	if err != nil {
		return nil, err
	}
	result, err := runner.Invoke(ctx, state, opts...)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (w *executableWorkflow[S]) ResumeJSON(ctx context.Context, request ResumeJSONRequest) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var state S
	if len(bytes.TrimSpace(request.State)) > 0 {
		if err := json.Unmarshal(request.State, &state); err != nil {
			return nil, err
		}
	}
	runner, err := runtime.NewRunner(w.graph, w.runnerOptions(ctx)...)
	if err != nil {
		return nil, err
	}
	result, err := runner.ResumeFrom(ctx, runtime.ResumeRequest[S]{
		Run:          request.Run,
		State:        state,
		ResumeNode:   request.ResumeNode,
		Response:     request.Response,
		InterruptID:  request.InterruptID,
		CheckpointID: request.CheckpointID,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (w *executableWorkflow[S]) runnerOptions(ctx context.Context) []runtime.Option[S] {
	runnerOptions := append([]runtime.Option[S](nil), w.options...)
	runnerOptions = append(runnerOptions, runtime.WithEventContext[S](runtime.EventContextOptions{Enabled: true}))
	if handler, ok := invokeEventHandlerFromContext(ctx); ok {
		runnerOptions = append(runnerOptions, runtime.WithEventHandler[S](handler))
	}
	return runnerOptions
}

type invokeEventHandlerKey struct{}

func withInvokeEventHandler(ctx context.Context, handler runtime.EventHandler) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if handler == nil {
		return ctx
	}
	return context.WithValue(ctx, invokeEventHandlerKey{}, handler)
}

func invokeEventHandlerFromContext(ctx context.Context) (runtime.EventHandler, bool) {
	if ctx == nil {
		return nil, false
	}
	handler, ok := ctx.Value(invokeEventHandlerKey{}).(runtime.EventHandler)
	return handler, ok && handler != nil
}
