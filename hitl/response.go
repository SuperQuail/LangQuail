package hitl

import (
	"encoding/json"
)

type Decision string

const (
	DecisionApproved Decision = "approved"
	DecisionRejected Decision = "rejected"
	DecisionProvided Decision = "provided"
)

type Response struct {
	InterruptID string          `json:"interrupt_id,omitempty"`
	Decision    Decision        `json:"decision"`
	Reason      string          `json:"reason,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

func Approve(payload any) Response {
	return Response{Decision: DecisionApproved, Payload: marshalPayload(payload)}
}

func Reject(reason string) Response {
	return Response{Decision: DecisionRejected, Reason: reason}
}

func Provide(payload any) Response {
	return Response{Decision: DecisionProvided, Payload: marshalPayload(payload)}
}

func DecodePayload[T any](response Response) (T, error) {
	var value T
	if len(response.Payload) == 0 {
		return value, nil
	}
	err := json.Unmarshal(response.Payload, &value)
	return value, err
}
