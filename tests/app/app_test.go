package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lq "github.com/superquail/langquail"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/tool/skill"
)

type appState struct {
	Query   string
	Calls   []tool.Call
	Results []tool.Result
	Writes  []llm.MessageWrite
}

type lookupInput struct {
	Query string `json:"query"`
}

type lookupOutput struct {
	Answer string `json:"answer"`
}

func TestVersion(t *testing.T) {
	if lq.Version != "1.0.0-alpha.2" {
		t.Fatalf("Version = %q, want %q", lq.Version, "1.0.0-alpha.2")
	}
}

func TestAppBuilderRegistersSkills(t *testing.T) {
	skillRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillRoot, "planner"), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "planner", "SKILL.md"), []byte("---\nname: planner\ndescription: Plan work\n---\nUse planning steps."), 0o644); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	manual := skill.NewRegistry()
	if err := manual.Register(skill.Skill{ID: "manual", Description: "Manual skill"}); err != nil {
		t.Fatalf("Register(manual) error = %v", err)
	}

	app, err := lq.New("project").
		SkillDirs(skillRoot).
		SkillRegistry(manual).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	registry := app.SkillRegistry()
	if registry == nil {
		t.Fatal("SkillRegistry() is nil")
	}
	ids := registry.IDs()
	if len(ids) != 2 || ids[0] != "manual" || ids[1] != "planner" {
		t.Fatalf("skill IDs = %#v", ids)
	}
	planner, exists := registry.Get("planner")
	if !exists || planner.Description != "Plan work" || !strings.Contains(planner.Instructions, "planning steps") {
		t.Fatalf("planner skill = %#v exists=%v", planner, exists)
	}
}

func TestAppBuilderRegistersComponentsAndContextRunsNodes(t *testing.T) {
	promptDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(promptDir, "support"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "support", "answer.md"), []byte("---\nrole: user\n---\nAnswer {{.Query}}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	provider := &appProvider{name: "fake"}
	lookup := tool.Define[lookupInput, lookupOutput]("lookup").
		Description("Lookup answer").
		InputSchema(map[string]any{"type": "object"}).
		Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
			return lookupOutput{Answer: "found:" + input.Query}, nil
		})

	answerWorkflow := graph.NewStateGraph[appState]("support.answer")
	answerWorkflow.Node(llm.Node("answer", llm.NodeSpec[appState]{
		Provider: "fake",
		Model:    "fake-model",
		PromptID: "support.answer",
		Data: func(ctx context.Context, state appState) (map[string]any, error) {
			return map[string]any{"Query": state.Query}, nil
		},
		ToolIDs: []string{"lookup"},
		Messages: llm.MessagePolicy[appState]{
			Write: func(ctx context.Context, state appState, write llm.MessageWrite) (appState, error) {
				state.Writes = append(state.Writes, write)
				return state, nil
			},
		},
	}))
	answerWorkflow.Start("answer")
	answerWorkflow.Finish("answer")

	toolWorkflow := graph.NewStateGraph[appState]("support.tools")
	toolWorkflow.Node(tool.Node("run_tool", tool.NodeSpec[appState]{
		ToolIDs: []string{"lookup"},
		Calls: func(ctx context.Context, state appState) ([]tool.Call, error) {
			return state.Calls, nil
		},
		Output: func(ctx context.Context, state appState, results []tool.Result) (graph.Command[appState], error) {
			state.Results = append(state.Results, results...)
			return graph.Update(state), nil
		},
	}))
	toolWorkflow.Start("run_tool")
	toolWorkflow.Finish("run_tool")

	app, err := lq.New("acme-ops").
		Providers(provider).
		Prompts(promptDir).
		Tools(lookup).
		Workflows(answerWorkflow, toolWorkflow).
		Store("sqlite:test.db").
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if app.ProjectID() != "acme-ops" || app.StoreConfig() != "sqlite:test.db" {
		t.Fatalf("app metadata = %#v store=%#v", app.ProjectID(), app.StoreConfig())
	}
	if app.PromptRegistry() == nil || app.ToolRegistry() == nil {
		t.Fatalf("registries = prompts:%v tools:%v", app.PromptRegistry(), app.ToolRegistry())
	}
	if _, exists := app.Workflow("support.answer"); !exists || len(app.Workflows()) != 2 {
		t.Fatalf("workflows = %#v exists=%v", app.Workflows(), exists)
	}
	snapshot, exists := app.Snapshot("support.answer")
	if !exists {
		t.Fatal("snapshot missing")
	}
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Metadata["prompt_id"] != "support.answer" || snapshot.Nodes[0].Metadata["tool_ids"] != "lookup" {
		t.Fatalf("snapshot nodes = %#v", snapshot.Nodes)
	}

	answerRunner, err := lqruntime.NewRunner(answerWorkflow)
	if err != nil {
		t.Fatalf("NewRunner(answer) error = %v", err)
	}
	answerResult, err := answerRunner.Invoke(app.Context(context.Background()), appState{Query: "pricing"})
	if err != nil {
		t.Fatalf("Invoke(answer) error = %v", err)
	}
	if answerResult.Run.Status != lqruntime.StatusCompleted || len(answerResult.State.Writes) != 1 {
		t.Fatalf("answer result = %#v", answerResult)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %#v", provider.requests)
	}
	request := provider.requests[0]
	if request.Provider != "fake" || request.Model != "fake-model" || request.Messages[0].Content != "Answer pricing" {
		t.Fatalf("llm request = %#v", request)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "lookup" {
		t.Fatalf("llm tools = %#v", request.Tools)
	}

	toolRunner, err := lqruntime.NewRunner(toolWorkflow)
	if err != nil {
		t.Fatalf("NewRunner(tool) error = %v", err)
	}
	toolResult, err := toolRunner.Invoke(app.Context(context.Background()), appState{Calls: []tool.Call{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"pricing"}`),
	}}})
	if err != nil {
		t.Fatalf("Invoke(tool) error = %v", err)
	}
	if len(toolResult.State.Results) != 1 || !strings.Contains(toolResult.State.Results[0].Content, "found:pricing") {
		t.Fatalf("tool results = %#v", toolResult.State.Results)
	}
}

func TestAppBuilderValidationErrors(t *testing.T) {
	t.Run("duplicate provider", func(t *testing.T) {
		_, err := lq.New("project").Providers(&appProvider{name: "fake"}, &appProvider{name: "fake"}).Build()
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("Build() error = %v", err)
		}
	})

	t.Run("duplicate tool", func(t *testing.T) {
		first := tool.Define[lookupInput, lookupOutput]("lookup").Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
			return lookupOutput{}, nil
		})
		second := tool.Define[lookupInput, lookupOutput]("lookup").Execute(func(ctx context.Context, input lookupInput) (lookupOutput, error) {
			return lookupOutput{}, nil
		})
		_, err := lq.New("project").Tools(first, second).Build()
		if err == nil || !strings.Contains(err.Error(), "duplicate tool") {
			t.Fatalf("Build() error = %v", err)
		}
	})

	t.Run("duplicate workflow", func(t *testing.T) {
		workflow := graph.NewStateGraph[appState]("workflow")
		workflow.Step("done", func(ctx context.Context, state appState) (graph.Command[appState], error) {
			return graph.Noop[appState](), nil
		})
		workflow.Start("done")
		workflow.Finish("done")
		_, err := lq.New("project").Workflows(workflow, workflow).Build()
		if err == nil || !strings.Contains(err.Error(), "duplicate workflow") {
			t.Fatalf("Build() error = %v", err)
		}
	})

	t.Run("prompt directory failure", func(t *testing.T) {
		_, err := lq.New("project").Prompts(filepath.Join(t.TempDir(), "missing")).Build()
		if err == nil {
			t.Fatal("Build() error is nil")
		}
	})

	t.Run("unsupported adjuster", func(t *testing.T) {
		_, err := lq.New("project").Adjuster(struct{}{}).Build()
		if err == nil || !strings.Contains(err.Error(), "adjuster must implement") {
			t.Fatalf("Build() error = %v", err)
		}
	})
}

func TestAppBuilderRegistersOptionalAdjusterInContext(t *testing.T) {
	adjuster := &appAdjuster{}
	app, err := lq.New("project").Adjuster(adjuster).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if app.LLMAdjuster() != adjuster || app.ToolAdjuster() != adjuster {
		t.Fatalf("adjusters = llm:%#v tool:%#v", app.LLMAdjuster(), app.ToolAdjuster())
	}
	ctx := app.Context(context.Background())
	if contextual, ok := llm.AdjusterFromContext(ctx); !ok || contextual != adjuster {
		t.Fatalf("llm adjuster from context = %#v ok=%v", contextual, ok)
	}
	if contextual, ok := tool.AdjusterFromContext(ctx); !ok || contextual != adjuster {
		t.Fatalf("tool adjuster from context = %#v ok=%v", contextual, ok)
	}
}

type appProvider struct {
	name     string
	requests []llm.Request
}

type appAdjuster struct{}

func (a *appAdjuster) BeforeLLM(ctx context.Context, request llm.BeforeLLMRequest) (llm.BeforeLLMResult, error) {
	return llm.BeforeLLMResult{}, nil
}

func (a *appAdjuster) AfterTool(ctx context.Context, request tool.AfterToolRequest) (tool.AfterToolResult, error) {
	return tool.AfterToolResult{}, nil
}

func (p *appProvider) Name() string {
	return p.name
}

func (p *appProvider) Chat(ctx context.Context, request llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, request)
	return llm.Response{
		ID:      "response",
		Model:   request.Model,
		Message: llm.Assistant("ok"),
		Text:    "ok",
	}, nil
}
