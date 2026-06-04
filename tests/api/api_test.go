package api_test

import (
	"context"
	"encoding/json"
	"testing"

	lq "github.com/superquail/langquail"
	"github.com/superquail/langquail/graph"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
)

type apiState struct {
	Value string
}

type apiToolInput struct {
	Query string `json:"query"`
}

type apiToolOutput struct {
	Answer string `json:"answer"`
}

func TestLangQuailPublicCommandHelpers(t *testing.T) {
	updated := lq.Update(apiState{Value: "next"})
	if updated.Update == nil || updated.Update.Value != "next" {
		t.Fatalf("Update() = %#v", updated)
	}

	gotoCommand := lq.Goto[apiState]("done")
	if gotoCommand.Goto != "done" || gotoCommand.Update != nil || gotoCommand.End {
		t.Fatalf("Goto() = %#v", gotoCommand)
	}

	end := lq.End[apiState]()
	if !end.End || end.Update != nil || end.Goto != "" {
		t.Fatalf("End() = %#v", end)
	}

	noop := lq.Noop[apiState]()
	if noop.Update != nil || noop.Goto != "" || noop.End || noop.Interrupt != nil {
		t.Fatalf("Noop() = %#v", noop)
	}
}

func TestLangQuailFacadeBuildsAndRunsWorkflow(t *testing.T) {
	g := lq.NewStateGraph[apiState]("api.facade.workflow")
	g.Step("start", func(ctx context.Context, state apiState) (graph.Command[apiState], error) {
		state.Value = "next"
		return lq.Update(state), nil
	})
	g.Start("start")
	g.Finish("start")

	runner, err := lq.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), apiState{Value: "initial"})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	if result.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if result.Run.WorkflowID != "api.facade.workflow" {
		t.Fatalf("WorkflowID = %q", result.Run.WorkflowID)
	}
	if result.State.Value != "next" {
		t.Fatalf("State = %#v", result.State)
	}
	if len(result.Checkpoints) != 1 {
		t.Fatalf("len(Checkpoints) = %d", len(result.Checkpoints))
	}
}

func TestLangQuailFacadeCreatesToolRegistry(t *testing.T) {
	registry := lq.NewToolRegistry()
	err := registry.Register(tool.Define[apiToolInput, apiToolOutput]("lookup").
		Description("Lookup answer").
		InputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		}).
		Execute(func(ctx context.Context, input apiToolInput) (apiToolOutput, error) {
			return apiToolOutput{Answer: "found:" + input.Query}, nil
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

	specs, err := registry.Specs("lookup")
	if err != nil {
		t.Fatalf("Specs() error = %v", err)
	}
	if len(specs) != 1 || specs[0].Name != "lookup" || specs[0].Description != "Lookup answer" {
		t.Fatalf("Specs() = %#v", specs)
	}

	llmSpecs, err := registry.LLMSpecs("lookup")
	if err != nil {
		t.Fatalf("LLMSpecs() error = %v", err)
	}
	if len(llmSpecs) != 1 || llmSpecs[0].Name != "lookup" || len(llmSpecs[0].InputSchema) == 0 {
		t.Fatalf("LLMSpecs() = %#v", llmSpecs)
	}
}
