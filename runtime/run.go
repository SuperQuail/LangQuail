package runtime

import (
	"time"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/trace"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

type Run struct {
	ID          string            `json:"id"`
	WorkflowID  string            `json:"workflow_id"`
	SessionID   string            `json:"session_id,omitempty"`
	Status      Status            `json:"status"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Error       string            `json:"error,omitempty"`
}

type Result[S any] struct {
	Run         Run                     `json:"run"`
	State       S                       `json:"state"`
	Events      []trace.Event           `json:"events"`
	Checkpoints []checkpoint.Checkpoint `json:"checkpoints"`
}
