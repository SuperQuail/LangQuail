package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"time"

	"github.com/superquail/langquail/internal/ids"
)

type Store interface {
	Save(context.Context, Checkpoint) (Checkpoint, error)
	Load(context.Context, string) (Checkpoint, error)
	List(context.Context, string) ([]Checkpoint, error)
	Latest(context.Context, string) (Checkpoint, bool, error)
}

type MemoryStore struct {
	mu    sync.Mutex
	byID  map[string]Checkpoint
	byRun map[string][]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		byID:  make(map[string]Checkpoint),
		byRun: make(map[string][]string),
	}
}

func (s *MemoryStore) Save(_ context.Context, checkpoint Checkpoint) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("checkpoint: nil MemoryStore")
	}
	if checkpoint.WorkflowID == "" {
		return Checkpoint{}, errors.New("checkpoint: workflow id is required")
	}
	if checkpoint.RunID == "" {
		return Checkpoint{}, errors.New("checkpoint: run id is required")
	}
	if checkpoint.NodeID == "" {
		return Checkpoint{}, errors.New("checkpoint: node id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if checkpoint.ID == "" {
		checkpoint.ID = ids.New("chk")
	}
	if _, exists := s.byID[checkpoint.ID]; exists {
		return Checkpoint{}, errors.New("checkpoint: duplicate checkpoint id")
	}
	if checkpoint.CreatedAt.IsZero() {
		checkpoint.CreatedAt = time.Now().UTC()
	}
	checkpoint.State = cloneRaw(checkpoint.State)
	s.byID[checkpoint.ID] = checkpoint
	s.byRun[checkpoint.RunID] = append(s.byRun[checkpoint.RunID], checkpoint.ID)
	return cloneCheckpoint(checkpoint), nil
}

func (s *MemoryStore) Load(_ context.Context, id string) (Checkpoint, error) {
	if s == nil {
		return Checkpoint{}, errors.New("checkpoint: nil MemoryStore")
	}
	if id == "" {
		return Checkpoint{}, errors.New("checkpoint: checkpoint id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	checkpoint, exists := s.byID[id]
	if !exists {
		return Checkpoint{}, errors.New("checkpoint: checkpoint not found")
	}
	return cloneCheckpoint(checkpoint), nil
}

func (s *MemoryStore) List(_ context.Context, runID string) ([]Checkpoint, error) {
	if s == nil {
		return nil, errors.New("checkpoint: nil MemoryStore")
	}
	if runID == "" {
		return nil, errors.New("checkpoint: run id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := s.byRun[runID]
	result := make([]Checkpoint, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneCheckpoint(s.byID[id]))
	}
	return result, nil
}

func (s *MemoryStore) Latest(ctx context.Context, runID string) (Checkpoint, bool, error) {
	list, err := s.List(ctx, runID)
	if err != nil {
		return Checkpoint{}, false, err
	}
	if len(list) == 0 {
		return Checkpoint{}, false, nil
	}
	return list[len(list)-1], true, nil
}

func cloneCheckpoint(checkpoint Checkpoint) Checkpoint {
	checkpoint.State = cloneRaw(checkpoint.State)
	return checkpoint
}

func cloneRaw(raw []byte) []byte {
	return bytes.Clone(raw)
}
