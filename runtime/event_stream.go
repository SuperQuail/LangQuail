package runtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/superquail/langquail/trace"
)

const defaultEventStreamBuffer = 256

type EventStream struct {
	mu      sync.RWMutex
	events  chan trace.Event
	closed  bool
	dropped atomic.Int64
}

func NewEventStream(buffer int) *EventStream {
	if buffer < 1 {
		buffer = defaultEventStreamBuffer
	}
	return &EventStream{events: make(chan trace.Event, buffer)}
}

func (s *EventStream) Handle(_ context.Context, event trace.Event) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil
	}
	select {
	case s.events <- event:
	default:
		s.dropped.Add(1)
	}
	return nil
}

func (s *EventStream) Events() <-chan trace.Event {
	if s == nil {
		ch := make(chan trace.Event)
		close(ch)
		return ch
	}
	return s.events
}

func (s *EventStream) Dropped() int64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

func (s *EventStream) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.events)
}
