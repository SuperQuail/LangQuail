package hitl

import (
	"encoding/json"
)

type RequestKind string

const (
	RequestKindHumanInput     RequestKind = "human_input"
	RequestKindToolPermission RequestKind = "tool_permission"
)

type Request struct {
	ID         string          `json:"id,omitempty"`
	Kind       RequestKind     `json:"kind"`
	Reason     string          `json:"reason,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

func NewRequest(kind RequestKind, reason string, payload any) Request {
	return Request{
		Kind:    kind,
		Reason:  reason,
		Payload: marshalPayload(payload),
	}
}

func marshalPayload(payload any) json.RawMessage {
	if payload == nil {
		return nil
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"payload_error":"marshal_failed"}`)
	}
	return bytes
}
