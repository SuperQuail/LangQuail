package tool

import "encoding/json"

func JSONSchema(value any) json.RawMessage {
	if value == nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage(bytes)
}
