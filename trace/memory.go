package trace

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/superquail/langquail/internal/ids"
)

type Recorder interface {
	Record(context.Context, Event) (Event, error)
	List(context.Context, string, int64) ([]Event, error)
}

type MemoryRecorder struct {
	mu     sync.Mutex
	next   map[string]int64
	events map[string][]Event
}

func NewMemoryRecorder() *MemoryRecorder {
	return &MemoryRecorder{
		next:   make(map[string]int64),
		events: make(map[string][]Event),
	}
}

func (r *MemoryRecorder) Record(_ context.Context, event Event) (Event, error) {
	if r == nil {
		return Event{}, errors.New("trace: nil MemoryRecorder")
	}
	if event.RunID == "" {
		return Event{}, errors.New("trace: run id is required")
	}
	if event.Type == "" {
		return Event{}, errors.New("trace: event type is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if event.ID == "" {
		event.ID = ids.New("evt")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	event = cloneEvent(event)
	// Sequence 只在单个 Run 内单调递增，后续持久化也按 RunID + Sequence replay。
	r.next[event.RunID]++
	event.Sequence = r.next[event.RunID]
	r.events[event.RunID] = append(r.events[event.RunID], event)
	return cloneEvent(event), nil
}

func (r *MemoryRecorder) List(_ context.Context, runID string, afterSequence int64) ([]Event, error) {
	if r == nil {
		return nil, errors.New("trace: nil MemoryRecorder")
	}
	if runID == "" {
		return nil, errors.New("trace: run id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var result []Event
	for _, event := range r.events[runID] {
		if event.Sequence > afterSequence {
			result = append(result, cloneEvent(event))
		}
	}
	return result, nil
}

func cloneEvent(event Event) Event {
	event.Payload = clonePayload(event.Payload)
	return event
}

func clonePayload(payload []byte) []byte {
	if payload == nil {
		return nil
	}
	return append([]byte(nil), payload...)
}
