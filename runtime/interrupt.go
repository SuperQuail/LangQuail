package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	"github.com/superquail/langquail/internal/ids"
	"github.com/superquail/langquail/trace"
)

type InterruptRecord struct {
	ID           string            `json:"id"`
	WorkflowID   string            `json:"workflow_id"`
	SessionID    string            `json:"session_id,omitempty"`
	RunID        string            `json:"run_id"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	NodeID       string            `json:"node_id"`
	ResumeNode   string            `json:"resume_node"`
	CheckpointID string            `json:"checkpoint_id"`
	Request      graph.Interrupt   `json:"request"`
	Response     *hitl.Response    `json:"response,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	ResolvedAt   time.Time         `json:"resolved_at,omitempty"`
}

type interruptStore interface {
	Save(context.Context, InterruptRecord) (InterruptRecord, error)
	Load(context.Context, string) (InterruptRecord, error)
	Resolve(context.Context, string, hitl.Response) (InterruptRecord, error)
}

type memoryInterruptStore struct {
	mu      sync.Mutex
	records map[string]InterruptRecord
}

func newMemoryInterruptStore() *memoryInterruptStore {
	return &memoryInterruptStore{records: make(map[string]InterruptRecord)}
}

func (s *memoryInterruptStore) Save(_ context.Context, record InterruptRecord) (InterruptRecord, error) {
	if s == nil {
		return InterruptRecord{}, errors.New("runtime: nil interrupt store")
	}
	if record.RunID == "" {
		return InterruptRecord{}, errors.New("runtime: interrupt run id is required")
	}
	if record.NodeID == "" {
		return InterruptRecord{}, errors.New("runtime: interrupt node id is required")
	}
	if record.CheckpointID == "" {
		return InterruptRecord{}, errors.New("runtime: interrupt checkpoint id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.ID == "" {
		record.ID = ids.New("int")
	}
	if _, exists := s.records[record.ID]; exists {
		return InterruptRecord{}, errors.New("runtime: duplicate interrupt id")
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.Request.ID = record.ID
	record.Request.RunID = record.RunID
	record.Request.NodeID = record.NodeID
	record.Request.CheckpointID = record.CheckpointID
	record.Request.ResumeNode = record.ResumeNode
	s.records[record.ID] = record
	return record, nil
}

func (s *memoryInterruptStore) Load(_ context.Context, id string) (InterruptRecord, error) {
	if s == nil {
		return InterruptRecord{}, errors.New("runtime: nil interrupt store")
	}
	if id == "" {
		return InterruptRecord{}, errors.New("runtime: interrupt id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[id]
	if !exists {
		return InterruptRecord{}, errors.New("runtime: interrupt not found")
	}
	return record, nil
}

func (s *memoryInterruptStore) Resolve(_ context.Context, id string, response hitl.Response) (InterruptRecord, error) {
	if s == nil {
		return InterruptRecord{}, errors.New("runtime: nil interrupt store")
	}
	if id == "" {
		return InterruptRecord{}, errors.New("runtime: interrupt id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.records[id]
	if !exists {
		return InterruptRecord{}, errors.New("runtime: interrupt not found")
	}
	if record.Response != nil {
		return InterruptRecord{}, errors.New("runtime: interrupt already resolved")
	}
	response.InterruptID = id
	record.Response = &response
	record.ResolvedAt = time.Now().UTC()
	s.records[id] = record
	return record, nil
}

func (r *Runner[S]) registerInterrupt(ctx context.Context, run Run, nodeID string, saved checkpoint.Checkpoint, interrupt *graph.Interrupt) (InterruptRecord, error) {
	if interrupt == nil {
		return InterruptRecord{}, errors.New("runtime: interrupt is required")
	}
	resumeNode := interrupt.ResumeNode
	if resumeNode == "" {
		resumeNode = nodeID
	}
	record := InterruptRecord{
		WorkflowID:   run.WorkflowID,
		SessionID:    run.SessionID,
		RunID:        run.ID,
		Metadata:     cloneMetadata(run.Metadata),
		NodeID:       nodeID,
		ResumeNode:   resumeNode,
		CheckpointID: saved.ID,
		Request:      *interrupt,
	}
	return r.interrupts.Save(ctx, record)
}

func (r *Runner[S]) Resume(ctx context.Context, interruptID string, response hitl.Response) (*Result[S], error) {
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := r.interrupts.Resolve(ctx, interruptID, response)
	if err != nil {
		return nil, err
	}
	saved, err := r.checkpoints.Load(ctx, record.CheckpointID)
	if err != nil {
		return nil, err
	}
	state, err := r.serializer.Unmarshal(saved.State)
	if err != nil {
		return nil, err
	}
	run := Run{
		ID:         record.RunID,
		WorkflowID: record.WorkflowID,
		SessionID:  record.SessionID,
		Status:     StatusRunning,
		Metadata:   cloneMetadata(record.Metadata),
		StartedAt:  time.Now().UTC(),
	}
	return r.run(ctx, run, state, record.ResumeNode, traceStart{
		eventType: trace.EventRunResumed,
		payload: map[string]any{
			"interrupt_id":  record.ID,
			"checkpoint_id": record.CheckpointID,
			"resume_node":   record.ResumeNode,
		},
	}, record.Response)
}

type traceStart struct {
	eventType string
	payload   any
}
