package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	"github.com/superquail/langquail/internal/ids"
	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/trace"
)

type Registry struct {
	mu    sync.RWMutex
	items map[string]Executable
}

func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Executable)}
}

func (r *Registry) Register(item Executable) error {
	if r == nil {
		return errors.New("tool: nil registry")
	}
	if item == nil {
		return errors.New("tool: nil executable")
	}
	spec := item.Spec()
	if spec.Name == "" {
		return errors.New("tool: tool name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.items[spec.Name]; exists {
		return fmt.Errorf("tool: duplicate tool %q", spec.Name)
	}
	r.items[spec.Name] = item
	return nil
}

func (r *Registry) Get(name string) (Executable, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	item, exists := r.items[name]
	return item, exists
}

func (r *Registry) Specs(names ...string) ([]Spec, error) {
	if r == nil {
		return nil, errors.New("tool: nil registry")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(names) == 0 {
		result := make([]Spec, 0, len(r.items))
		for _, item := range r.items {
			result = append(result, item.Spec())
		}
		return result, nil
	}
	result := make([]Spec, 0, len(names))
	for _, name := range names {
		item, exists := r.items[name]
		if !exists {
			return nil, fmt.Errorf("tool: tool %q is not registered", name)
		}
		result = append(result, item.Spec())
	}
	return result, nil
}

func (r *Registry) LLMSpecs(names ...string) ([]llm.ToolSpec, error) {
	specs, err := r.Specs(names...)
	if err != nil {
		return nil, err
	}
	result := make([]llm.ToolSpec, 0, len(specs))
	for _, spec := range specs {
		result = append(result, llm.ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: spec.InputSchema,
		})
	}
	return result, nil
}

type NodeSpec[S any] struct {
	Registry         *Registry
	ToolIDs          []string
	Calls            func(context.Context, S) ([]Call, error)
	Output           func(context.Context, S, []Result) (graph.Command[S], error)
	Error            func(context.Context, S, Call, error) (graph.Command[S], error)
	ContinueOnError  bool
	ProgressInterval time.Duration
	Metadata         map[string]string
}

func Node[S any](id string, spec NodeSpec[S]) graph.NodeSpec[S] {
	metadata := map[string]string{"node": "tool"}
	if len(spec.ToolIDs) > 0 {
		metadata["tool_ids"] = strings.Join(spec.ToolIDs, ",")
	}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	return graph.NodeSpec[S]{
		ID:       id,
		Kind:     graph.NodeKindTool,
		Metadata: metadata,
		Run: func(ctx context.Context, state S) (graph.Command[S], error) {
			registry := spec.Registry
			if registry == nil {
				registry, _ = RegistryFromContext(ctx)
			}
			if registry == nil {
				return graph.Noop[S](), errors.New("tool: registry is required")
			}
			if spec.Calls == nil {
				return graph.Noop[S](), errors.New("tool: call reader is required")
			}
			calls, err := spec.Calls(ctx, state)
			if err != nil {
				return graph.Noop[S](), err
			}
			results := make([]Result, 0, len(calls))
			for _, call := range calls {
				call = normalizeCall(call)
				if err := requireAllowedTool(call.Name, spec.ToolIDs); err != nil {
					return graph.Noop[S](), err
				}
				result, command, err := executeCall[S](ctx, id, spec.Metadata, spec.ProgressInterval, registry, call)
				if err != nil {
					if spec.Error != nil {
						return spec.Error(ctx, state, call, err)
					}
					if spec.ContinueOnError {
						results = append(results, ErrorResult(call, err))
						continue
					}
					return graph.Noop[S](), err
				}
				if command.Interrupt != nil {
					return command, nil
				}
				results = append(results, result)
			}
			if spec.Output == nil {
				return graph.Noop[S](), nil
			}
			return spec.Output(ctx, state, results)
		},
	}
}

type registryContextKey struct{}

func WithRegistry(ctx context.Context, registry *Registry) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if registry == nil {
		return ctx
	}
	return context.WithValue(ctx, registryContextKey{}, registry)
}

func RegistryFromContext(ctx context.Context) (*Registry, bool) {
	if ctx == nil {
		return nil, false
	}
	registry, ok := ctx.Value(registryContextKey{}).(*Registry)
	return registry, ok && registry != nil
}

func requireAllowedTool(name string, allowed []string) error {
	if len(allowed) == 0 {
		return nil
	}
	for _, id := range allowed {
		if id == name {
			return nil
		}
	}
	return fmt.Errorf("tool: tool %q is not allowed for this node", name)
}

func normalizeCall(call Call) Call {
	if call.ID == "" {
		call.ID = ids.New("call")
	}
	return call
}

type completedEventPayload struct {
	Result
	DurationMS int64 `json:"duration_ms"`
}

func executeCall[S any](ctx context.Context, nodeID string, metadata map[string]string, progressInterval time.Duration, registry *Registry, call Call) (Result, graph.Command[S], error) {
	item, exists := registry.Get(call.Name)
	if !exists {
		return Result{}, graph.Noop[S](), fmt.Errorf("tool: tool %q is not registered", call.Name)
	}
	if request, required, err := item.PermissionJSON(ctx, call.Arguments); err != nil {
		return Result{}, graph.Noop[S](), err
	} else if required {
		if response, ok := hitl.ResponseFromContext(ctx); ok {
			if response.Decision == hitl.DecisionRejected {
				return Result{}, graph.Noop[S](), ErrPermissionDenied
			}
		} else {
			request.ToolName = call.Name
			request.ToolCallID = call.ID
			return Result{}, graph.Command[S]{
				Interrupt: &graph.Interrupt{
					Kind:    string(request.Kind),
					Reason:  request.Reason,
					Payload: request,
				},
			}, nil
		}
	}
	startedAt := time.Now()
	if _, err := trace.Emit(ctx, trace.EventToolStarted, call); err != nil {
		return Result{}, graph.Noop[S](), err
	}
	stopProgress := startProgressTicker(ctx, call, startedAt, progressInterval)
	result, err := item.ExecuteJSON(ctx, call.Arguments)
	stopProgress()
	if err != nil {
		_, _ = trace.Emit(ctx, trace.EventToolFailed, map[string]any{
			"tool":        call.Name,
			"call_id":     call.ID,
			"duration_ms": elapsedMilliseconds(startedAt),
			"error":       err.Error(),
		})
		return Result{}, graph.Noop[S](), err
	}
	result.CallID = call.ID
	result.Name = call.Name
	result, completedContext, err := applyAfterToolAdjuster(ctx, nodeID, metadata, call, result)
	if err != nil {
		return Result{}, graph.Noop[S](), err
	}
	emitCtx := ctx
	if completedContext == nil {
		completedContext = &trace.EventContext{
			Current: trace.ContextSnapshot{
				ToolResult: trace.Payload(result),
			},
		}
	}
	emitCtx = trace.WithEventContext(ctx, completedContext)
	if _, err := trace.Emit(emitCtx, trace.EventToolCompleted, completedEventPayload{
		Result:     result,
		DurationMS: elapsedMilliseconds(startedAt),
	}); err != nil {
		return Result{}, graph.Noop[S](), err
	}
	return result, graph.Noop[S](), nil
}

func applyAfterToolAdjuster(ctx context.Context, nodeID string, metadata map[string]string, call Call, result Result) (Result, *trace.EventContext, error) {
	adjuster, ok := AdjusterFromContext(ctx)
	if !ok {
		return result, nil, nil
	}
	adjusted, err := adjuster.AfterTool(ctx, AfterToolRequest{
		NodeID:   nodeID,
		Call:     cloneCall(call),
		Result:   cloneResult(result),
		Metadata: cloneStringMap(metadata),
	})
	if err != nil {
		return Result{}, nil, err
	}
	if adjusted.Result == nil {
		return result, nil, nil
	}
	before := cloneResult(result)
	next := cloneResult(*adjusted.Result)
	if next.CallID == "" {
		next.CallID = call.ID
	}
	if next.Name == "" {
		next.Name = call.Name
	}
	return next, &trace.EventContext{
		Current: trace.ContextSnapshot{
			ToolResult: trace.Payload(next),
		},
		Change: &trace.ContextChange{
			Before: trace.Payload(before),
			After:  trace.Payload(next),
		},
	}, nil
}

func ErrorResult(call Call, err error) Result {
	message := ""
	if err != nil {
		message = err.Error()
	}
	raw, marshalErr := json.Marshal(map[string]string{
		"error": message,
		"tool":  call.Name,
	})
	content := string(raw)
	if marshalErr != nil {
		content = message
	}
	return Result{
		CallID:  call.ID,
		Name:    call.Name,
		Content: content,
		Raw:     raw,
		Error:   message,
	}
}
