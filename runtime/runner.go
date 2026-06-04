package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	"github.com/superquail/langquail/internal/ids"
	"github.com/superquail/langquail/trace"
)

type Runner[S any] struct {
	graph       *graph.StateGraph[S]
	recorder    trace.Recorder
	checkpoints checkpoint.Store
	serializer  checkpoint.Serializer[S]
	interrupts  interruptStore
	onEvent     EventHandler
	maxSteps    int
}

const DefaultMaxSteps = 10000

func NewRunner[S any](stateGraph *graph.StateGraph[S], opts ...Option[S]) (*Runner[S], error) {
	if stateGraph == nil {
		return nil, errors.New("runtime: graph is required")
	}
	if err := stateGraph.Validate(); err != nil {
		return nil, err
	}

	runner := &Runner[S]{
		graph:       stateGraph,
		recorder:    trace.NewMemoryRecorder(),
		checkpoints: checkpoint.NewMemoryStore(),
		serializer:  checkpoint.NewJSONSerializer[S](),
		interrupts:  newMemoryInterruptStore(),
		maxSteps:    DefaultMaxSteps,
	}
	for _, opt := range opts {
		opt(runner)
	}
	if runner.maxSteps < 0 {
		return nil, errors.New("runtime: max steps cannot be negative")
	}
	if runner.recorder == nil {
		return nil, errors.New("runtime: recorder is required")
	}
	if runner.checkpoints == nil {
		return nil, errors.New("runtime: checkpoint store is required")
	}
	if runner.serializer == nil {
		return nil, errors.New("runtime: checkpoint serializer is required")
	}
	if runner.interrupts == nil {
		return nil, errors.New("runtime: interrupt store is required")
	}
	return runner, nil
}

func (r *Runner[S]) Invoke(ctx context.Context, initialState S, opts ...InvokeOption) (*Result[S], error) {
	if ctx == nil {
		ctx = context.Background()
	}

	config := invokeConfig{
		runID:   ids.New("run"),
		startAt: r.graph.StartNode(),
	}
	for _, opt := range opts {
		opt(&config)
	}
	if config.runID == "" {
		config.runID = ids.New("run")
	}
	if config.startAt == "" {
		config.startAt = r.graph.StartNode()
	}
	if !r.graph.HasNode(config.startAt) {
		return nil, fmt.Errorf("runtime: start node %q is not registered", config.startAt)
	}

	run := Run{
		ID:         config.runID,
		WorkflowID: r.graph.WorkflowID(),
		SessionID:  config.sessionID,
		Status:     StatusQueued,
		Metadata:   cloneMetadata(config.metadata),
		StartedAt:  time.Now().UTC(),
	}
	return r.run(ctx, run, initialState, config.startAt, traceStart{
		eventType: trace.EventRunStarted,
		payload: map[string]any{
			"start_at": config.startAt,
		},
	}, nil)
}

func (r *Runner[S]) run(ctx context.Context, run Run, initialState S, startAt string, start traceStart, resumeResponse *hitl.Response) (*Result[S], error) {
	state := initialState
	events := make([]trace.Event, 0, 16)

	run.Status = StatusRunning
	if _, err := r.record(ctx, &events, run, "", start.eventType, start.payload); err != nil {
		return r.result(run, state, events), err
	}

	current := startAt
	steps := 0
	for {
		if err := ctx.Err(); err != nil {
			return r.cancel(run, state, events, current, err)
		}
		if r.maxSteps > 0 {
			steps++
			if steps > r.maxSteps {
				return r.fail(run, state, events, current, fmt.Errorf("runtime: max steps exceeded: %d", r.maxSteps))
			}
		}

		nodeFunc, exists := r.graph.NodeFunc(current)
		if !exists {
			return r.fail(run, state, events, current, fmt.Errorf("runtime: node %q is not registered", current))
		}

		if _, err := r.record(ctx, &events, run, current, trace.EventNodeStarted, nil); err != nil {
			return r.result(run, state, events), err
		}

		nodeCtx := trace.WithEmitter(ctx, func(emitCtx context.Context, eventType string, payload any) (trace.Event, error) {
			return r.record(emitCtx, &events, run, current, eventType, payload)
		})
		if resumeResponse != nil {
			nodeCtx = hitl.WithResponse(nodeCtx, *resumeResponse)
			resumeResponse = nil
		}

		command, err := nodeFunc(nodeCtx, state)
		if err != nil {
			if cancelErr := cancellationCause(ctx, err); cancelErr != nil {
				return r.cancel(run, state, events, current, cancelErr)
			}
			_, _ = r.record(context.Background(), &events, run, current, trace.EventNodeFailed, map[string]any{
				"error": err.Error(),
			})
			return r.fail(run, state, events, current, err)
		}
		if command.Update != nil {
			state = *command.Update
		}

		completed, err := r.record(ctx, &events, run, current, trace.EventNodeCompleted, nil)
		if err != nil {
			return r.result(run, state, events), err
		}
		saved, err := r.saveCheckpoint(ctx, run, current, completed.Sequence, state)
		if err != nil {
			return r.fail(run, state, events, current, err)
		}
		if _, err := r.record(ctx, &events, run, current, trace.EventCheckpointSaved, map[string]any{
			"checkpoint_id": saved.ID,
			"sequence":      saved.Sequence,
		}); err != nil {
			return r.result(run, state, events), err
		}

		if command.Interrupt != nil {
			record, err := r.registerInterrupt(ctx, run, current, saved, command.Interrupt)
			if err != nil {
				return r.fail(run, state, events, current, err)
			}
			_, _ = r.record(context.Background(), &events, run, current, trace.EventInterruptCreated, record.Request)
			return r.interrupt(run, state, events, current, record)
		}

		// 路由优先级：Command.End > Command.Goto > Finish 节点 > 条件路由 > 固定边。
		if command.End {
			return r.complete(run, state, events, current)
		}
		if command.Goto != "" {
			if !r.graph.HasNode(command.Goto) {
				return r.fail(run, state, events, current, fmt.Errorf("runtime: goto target %q is not registered", command.Goto))
			}
			if _, err := r.record(ctx, &events, run, current, trace.EventEdgeSelected, map[string]any{
				"from": current,
				"to":   command.Goto,
				"kind": string(graph.EdgeKindDynamic),
			}); err != nil {
				return r.result(run, state, events), err
			}
			current = command.Goto
			continue
		}
		if r.graph.IsFinish(current) {
			return r.complete(run, state, events, current)
		}

		selection, selected, err := r.graph.SelectRoute(ctx, current, state)
		if err != nil {
			return r.fail(run, state, events, current, err)
		}
		if selected {
			if _, err := r.record(ctx, &events, run, current, trace.EventEdgeSelected, map[string]any{
				"from":    current,
				"to":      selection.Target,
				"kind":    string(selection.Kind),
				"order":   selection.Order,
				"default": selection.Default,
			}); err != nil {
				return r.result(run, state, events), err
			}
			current = selection.Target
			continue
		}

		targets := r.graph.FixedTargets(current)
		switch len(targets) {
		case 0:
			return r.fail(run, state, events, current, fmt.Errorf("runtime: node %q has no route to continue", current))
		case 1:
			if _, err := r.record(ctx, &events, run, current, trace.EventEdgeSelected, map[string]any{
				"from": current,
				"to":   targets[0],
				"kind": string(graph.EdgeKindFixed),
			}); err != nil {
				return r.result(run, state, events), err
			}
			current = targets[0]
		default:
			return r.fail(run, state, events, current, fmt.Errorf("runtime: node %q has multiple fixed edges", current))
		}
	}
}

func (r *Runner[S]) record(ctx context.Context, events *[]trace.Event, run Run, nodeID string, eventType string, payload any) (trace.Event, error) {
	event, err := r.recorder.Record(ctx, trace.Event{
		Type:       eventType,
		WorkflowID: run.WorkflowID,
		SessionID:  run.SessionID,
		RunID:      run.ID,
		NodeID:     nodeID,
		Payload:    trace.Payload(payload),
	})
	if err != nil {
		return trace.Event{}, err
	}
	*events = append(*events, event)
	if r.onEvent != nil {
		if err := r.onEvent(ctx, event); err != nil {
			return trace.Event{}, err
		}
	}
	return event, nil
}

func (r *Runner[S]) saveCheckpoint(ctx context.Context, run Run, nodeID string, sequence int64, state S) (checkpoint.Checkpoint, error) {
	snapshot, err := r.serializer.Marshal(state)
	if err != nil {
		return checkpoint.Checkpoint{}, err
	}
	return r.checkpoints.Save(ctx, checkpoint.Checkpoint{
		WorkflowID: run.WorkflowID,
		SessionID:  run.SessionID,
		RunID:      run.ID,
		NodeID:     nodeID,
		Sequence:   sequence,
		State:      snapshot,
	})
}

func (r *Runner[S]) complete(run Run, state S, events []trace.Event, nodeID string) (*Result[S], error) {
	run.Status = StatusCompleted
	run.CompletedAt = time.Now().UTC()
	_, _ = r.record(context.Background(), &events, run, nodeID, trace.EventRunCompleted, nil)
	return r.result(run, state, events), nil
}

func (r *Runner[S]) fail(run Run, state S, events []trace.Event, nodeID string, cause error) (*Result[S], error) {
	run.Status = StatusFailed
	run.Error = cause.Error()
	run.CompletedAt = time.Now().UTC()
	_, _ = r.record(context.Background(), &events, run, nodeID, trace.EventRunFailed, map[string]any{
		"error": cause.Error(),
	})
	return r.result(run, state, events), cause
}

func (r *Runner[S]) cancel(run Run, state S, events []trace.Event, nodeID string, cause error) (*Result[S], error) {
	run.Status = StatusCancelled
	run.Error = cause.Error()
	run.CompletedAt = time.Now().UTC()
	_, _ = r.record(context.Background(), &events, run, nodeID, trace.EventRunCancelled, map[string]any{
		"error": cause.Error(),
	})
	return r.result(run, state, events), cause
}

func (r *Runner[S]) interrupt(run Run, state S, events []trace.Event, nodeID string, interrupt InterruptRecord) (*Result[S], error) {
	run.Status = StatusInterrupted
	run.CompletedAt = time.Now().UTC()
	_, _ = r.record(context.Background(), &events, run, nodeID, trace.EventRunInterrupted, interrupt.Request)
	return r.result(run, state, events), nil
}

func (r *Runner[S]) result(run Run, state S, events []trace.Event) *Result[S] {
	checkpoints, _ := r.checkpoints.List(context.Background(), run.ID)
	if recorded, err := r.recorder.List(context.Background(), run.ID, 0); err == nil && len(recorded) > 0 {
		events = recorded
	}
	return &Result[S]{
		Run:         run,
		State:       state,
		Events:      append([]trace.Event(nil), events...),
		Checkpoints: checkpoints,
	}
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

func cancellationCause(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return ctx.Err()
}
