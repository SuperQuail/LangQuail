package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/superquail/langquail/hitl"
)

type Spec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type Executable interface {
	Spec() Spec
	ExecuteJSON(context.Context, json.RawMessage) (Result, error)
	PermissionJSON(context.Context, json.RawMessage) (hitl.Request, bool, error)
}

type ExecuteFunc[TIn, TOut any] func(context.Context, TIn) (TOut, error)

type Definition[TIn, TOut any] struct {
	id          string
	description string
	inputSchema json.RawMessage
	execute     ExecuteFunc[TIn, TOut]
	permission  PermissionPolicy[TIn]
}

func Define[TIn, TOut any](id string) *Definition[TIn, TOut] {
	return &Definition[TIn, TOut]{id: id, inputSchema: JSONSchema(nil)}
}

func (d *Definition[TIn, TOut]) Description(text string) *Definition[TIn, TOut] {
	d.description = text
	return d
}

func (d *Definition[TIn, TOut]) InputSchema(schema any) *Definition[TIn, TOut] {
	d.inputSchema = JSONSchema(schema)
	return d
}

func (d *Definition[TIn, TOut]) Permission(policy PermissionPolicy[TIn]) *Definition[TIn, TOut] {
	d.permission = policy
	return d
}

func (d *Definition[TIn, TOut]) Execute(fn ExecuteFunc[TIn, TOut]) *Definition[TIn, TOut] {
	d.execute = fn
	return d
}

func (d *Definition[TIn, TOut]) Spec() Spec {
	if d == nil {
		return Spec{}
	}
	return Spec{
		Name:        d.id,
		Description: d.description,
		InputSchema: json.RawMessage(bytes.Clone(d.inputSchema)),
	}
}

func (d *Definition[TIn, TOut]) ExecuteJSON(ctx context.Context, input json.RawMessage) (Result, error) {
	if d == nil {
		return Result{}, errors.New("tool: nil definition")
	}
	if d.execute == nil {
		return Result{}, errors.New("tool: execute function is required")
	}
	typed, err := decodeInput[TIn](input)
	if err != nil {
		return Result{}, err
	}
	output, err := d.execute(ctx, typed)
	if err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Name:    d.id,
		Content: resultContent(output, raw),
		Raw:     raw,
	}, nil
}

func (d *Definition[TIn, TOut]) PermissionJSON(ctx context.Context, input json.RawMessage) (hitl.Request, bool, error) {
	if d == nil || d.permission == nil {
		return hitl.Request{}, false, nil
	}
	typed, err := decodeInput[TIn](input)
	if err != nil {
		return hitl.Request{}, false, err
	}
	return d.permission(ctx, typed)
}

func decodeInput[T any](input json.RawMessage) (T, error) {
	var value T
	if len(input) == 0 {
		return value, nil
	}
	err := json.Unmarshal(input, &value)
	return value, err
}
