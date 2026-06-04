package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/tool"
)

type lookupInput struct {
	Query string `json:"query"`
}

type lookupOutput struct {
	Answer string `json:"answer"`
}

func TestRegistryExecuteTypedTool(t *testing.T) {
	registry := tool.NewRegistry()
	err := registry.Register(tool.Define[lookupInput, lookupOutput]("lookup").
		Description("Lookup answer").
		InputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		}).
		Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
			return lookupOutput{Answer: "found:" + input.Query}, nil
		}))
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	item, exists := registry.Get("lookup")
	if !exists {
		t.Fatal("lookup not registered")
	}
	result, err := item.ExecuteJSON(context.Background(), json.RawMessage(`{"query":"langquail"}`))
	if err != nil {
		t.Fatalf("ExecuteJSON() error = %v", err)
	}
	if result.Content != `{"answer":"found:langquail"}` {
		t.Fatalf("Content = %q", result.Content)
	}

	specs, err := registry.LLMSpecs("lookup")
	if err != nil {
		t.Fatalf("LLMSpecs() error = %v", err)
	}
	if len(specs) != 1 || specs[0].Name != "lookup" {
		t.Fatalf("specs = %#v", specs)
	}
}

func TestRegistryRejectsDuplicateTool(t *testing.T) {
	registry := tool.NewRegistry()
	first := tool.Define[lookupInput, lookupOutput]("lookup").Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
		return lookupOutput{}, nil
	})
	second := tool.Define[lookupInput, lookupOutput]("lookup").Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
		return lookupOutput{}, nil
	})
	if err := registry.Register(first); err != nil {
		t.Fatalf("Register(first) error = %v", err)
	}
	if err := registry.Register(second); err == nil {
		t.Fatal("Register(second) error = nil, want duplicate error")
	}
}
