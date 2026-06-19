package api

import (
	"sync"

	"github.com/superquail/langquail/trace"
)

type EventFilter struct {
	WorkflowID string
	SessionID  string
	RunID      string
	NodeID     string
}

type EventHub struct {
	mu          sync.Mutex
	next        int64
	subscribers map[int64]*EventSubscription
}

type EventSubscription struct {
	hub    *EventHub
	id     int64
	filter EventFilter
	events chan trace.Event
	once   sync.Once
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[int64]*EventSubscription)}
}

func (h *EventHub) Subscribe(filter EventFilter) *EventSubscription {
	if h == nil {
		return &EventSubscription{events: make(chan trace.Event)}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	sub := &EventSubscription{
		hub:    h,
		id:     h.next,
		filter: filter,
		events: make(chan trace.Event, 256),
	}
	h.subscribers[sub.id] = sub
	return sub
}

func (h *EventHub) Publish(event trace.Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	for _, sub := range h.subscribers {
		if sub.matches(event) {
			select {
			case sub.events <- event:
			default:
			}
		}
	}
	h.mu.Unlock()
}

func (s *EventSubscription) Events() <-chan trace.Event {
	if s == nil {
		ch := make(chan trace.Event)
		close(ch)
		return ch
	}
	return s.events
}

func (s *EventSubscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.hub != nil {
			s.hub.mu.Lock()
			delete(s.hub.subscribers, s.id)
			s.hub.mu.Unlock()
		}
		close(s.events)
	})
}

func (s *EventSubscription) matches(event trace.Event) bool {
	if s == nil {
		return false
	}
	if s.filter.WorkflowID != "" && event.WorkflowID != s.filter.WorkflowID {
		return false
	}
	if s.filter.SessionID != "" && event.SessionID != s.filter.SessionID {
		return false
	}
	if s.filter.RunID != "" && event.RunID != s.filter.RunID {
		return false
	}
	if s.filter.NodeID != "" && event.NodeID != s.filter.NodeID {
		return false
	}
	return true
}
