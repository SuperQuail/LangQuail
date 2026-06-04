package checkpoint

import (
	"encoding/json"
	"time"
)

type Checkpoint struct {
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id,omitempty"`
	WorkflowID string          `json:"workflow_id"`
	SessionID  string          `json:"session_id,omitempty"`
	RunID      string          `json:"run_id"`
	NodeID     string          `json:"node_id"`
	ParentID   string          `json:"parent_id,omitempty"`
	Sequence   int64           `json:"sequence"`
	State      json.RawMessage `json:"state"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Serializer[S any] interface {
	Marshal(S) (json.RawMessage, error)
	Unmarshal(json.RawMessage) (S, error)
}

type JSONSerializer[S any] struct{}

func NewJSONSerializer[S any]() JSONSerializer[S] {
	return JSONSerializer[S]{}
}

func (JSONSerializer[S]) Marshal(state S) (json.RawMessage, error) {
	// Checkpoint 保存状态快照，不保存事件日志；JSON 便于阶段三迁移到持久化。
	bytes, err := json.Marshal(state)
	return json.RawMessage(bytes), err
}

func (JSONSerializer[S]) Unmarshal(snapshot json.RawMessage) (S, error) {
	var state S
	if len(snapshot) == 0 {
		return state, nil
	}
	err := json.Unmarshal(snapshot, &state)
	return state, err
}
