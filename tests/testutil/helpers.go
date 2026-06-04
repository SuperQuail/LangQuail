package testutil

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/trace"
)

func InterruptIDFromEvents(t testing.TB, events []trace.Event) string {
	t.Helper()
	for _, event := range events {
		if event.Type != trace.EventInterruptCreated {
			continue
		}
		var interrupt graph.Interrupt
		if err := json.Unmarshal(event.Payload, &interrupt); err != nil {
			t.Fatalf("decode interrupt: %v", err)
		}
		if interrupt.ID == "" {
			t.Fatal("interrupt id is empty")
		}
		return interrupt.ID
	}
	t.Fatal("interrupt event not found")
	return ""
}

type HandlerErrors struct {
	t        testing.TB
	mu       sync.Mutex
	messages []string
}

func NewHandlerErrors(t testing.TB) *HandlerErrors {
	t.Helper()
	return &HandlerErrors{t: t}
}

func (h *HandlerErrors) Errorf(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, fmt.Sprintf(format, args...))
}

func (h *HandlerErrors) Failf(w http.ResponseWriter, format string, args ...any) {
	h.Errorf(format, args...)
	http.Error(w, "handler assertion failed", http.StatusInternalServerError)
}

func (h *HandlerErrors) AssertNone() {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.messages) > 0 {
		h.t.Fatalf("handler errors:\n%s", strings.Join(h.messages, "\n"))
	}
}
