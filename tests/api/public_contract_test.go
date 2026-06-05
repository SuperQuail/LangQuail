package api_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/superquail/langquail/checkpoint"
	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/hitl"
	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/trace"
)

var (
	_ checkpoint.Store   = checkpoint.NewMemoryStore()
	_ trace.Recorder     = trace.NewMemoryRecorder()
	_ llm.Provider       = contractProvider{}
	_ llm.StreamProvider = contractStreamProvider{}
	_ tool.Executable    = tool.Define[apiToolInput, apiToolOutput]("compile_contract")
)

type contractProvider struct {
	name string
}

func (p contractProvider) Name() string {
	return p.name
}

func (p contractProvider) Chat(_ context.Context, request llm.Request) (llm.Response, error) {
	return llm.Response{
		ID:      "resp_contract",
		Model:   request.Model,
		Message: llm.Assistant("ok"),
		Text:    "ok",
	}, nil
}

type contractStreamProvider struct {
	contractProvider
}

func (p contractStreamProvider) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	if handler != nil {
		if err := handler(ctx, llm.StreamChunk{Text: "ok"}); err != nil {
			return llm.Response{}, err
		}
		if err := handler(ctx, llm.StreamChunk{Done: true}); err != nil {
			return llm.Response{}, err
		}
	}
	return p.Chat(ctx, request)
}

func TestGraphPublicCommandAndSnapshotContracts(t *testing.T) {
	command := graph.UpdateAndGoto(apiState{Value: "next"}, "done")
	if command.Update == nil || command.Update.Value != "next" || command.Goto != "done" || command.End {
		t.Fatalf("UpdateAndGoto() = %#v", command)
	}

	interrupt := graph.InterruptRun[apiState]("needs review", map[string]string{"kind": "contract"})
	if interrupt.Interrupt == nil || interrupt.Interrupt.Reason != "needs review" {
		t.Fatalf("InterruptRun() = %#v", interrupt)
	}
	payload, ok := interrupt.Interrupt.Payload.(map[string]string)
	if !ok || payload["kind"] != "contract" {
		t.Fatalf("interrupt payload = %#v", interrupt.Interrupt.Payload)
	}

	metadata := map[string]string{"component": "contract"}
	g := graph.NewStateGraph[apiState]("api.graph.contract")
	g.Node(graph.NodeSpec[apiState]{
		ID:       "start",
		Kind:     graph.NodeKindStep,
		Metadata: metadata,
		Run: func(ctx context.Context, state apiState) (graph.Command[apiState], error) {
			return graph.Noop[apiState](), nil
		},
	})
	metadata["component"] = "mutated"
	g.Start("start")
	g.Finish("start")
	if err := g.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	snapshot := g.Snapshot()
	if len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Metadata["component"] != "contract" {
		t.Fatalf("Snapshot() = %#v", snapshot)
	}
	snapshot.Nodes[0].Metadata["component"] = "snapshot-mutated"
	again := g.Snapshot()
	if again.Nodes[0].Metadata["component"] != "contract" {
		t.Fatalf("Snapshot() metadata was externally mutated: %#v", again.Nodes[0].Metadata)
	}
}

func TestLLMPublicMessageAndProviderContracts(t *testing.T) {
	if llm.System("system").Role != llm.RoleSystem {
		t.Fatalf("System() role mismatch")
	}
	if llm.Developer("developer").Role != llm.RoleDeveloper {
		t.Fatalf("Developer() role mismatch")
	}
	if llm.User("user").Role != llm.RoleUser {
		t.Fatalf("User() role mismatch")
	}
	if llm.Assistant("assistant").Role != llm.RoleAssistant {
		t.Fatalf("Assistant() role mismatch")
	}
	if string(llm.ToolChoiceAuto) != "auto" || string(llm.ToolChoiceNone) != "none" || string(llm.ToolChoiceRequired) != "required" {
		t.Fatalf("tool choice constants changed")
	}

	calls := []llm.ToolCall{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}
	assistant := llm.AssistantToolCalls("use tool", calls)
	calls[0].Name = "mutated"
	calls[0].Arguments[0] = '['
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Name != "lookup" || string(assistant.ToolCalls[0].Arguments) != `{"query":"langquail"}` {
		t.Fatalf("AssistantToolCalls() did not clone calls: %#v", assistant.ToolCalls)
	}
	assistant.ToolCalls[0].Name = "changed"
	assistant.ToolCalls[0].Arguments[0] = ']'
	if calls[0].Name != "mutated" || calls[0].Arguments[0] != '[' {
		t.Fatalf("mutating message calls changed source calls: %#v", calls)
	}

	toolMessage := llm.ToolResult("call_1", `{"answer":"ok"}`)
	if toolMessage.Role != llm.RoleTool || toolMessage.ToolCallID != "call_1" || toolMessage.Content == "" {
		t.Fatalf("ToolResult() = %#v", toolMessage)
	}

	providers := llm.Providers(contractProvider{name: "alpha"}, contractProvider{})
	names := providers.Names()
	sort.Strings(names)
	if len(names) != 1 || names[0] != "alpha" {
		t.Fatalf("Names() = %#v", names)
	}
	provider, err := providers.Get("alpha")
	if err != nil {
		t.Fatalf("Get(alpha) error = %v", err)
	}
	response, err := provider.Chat(context.Background(), llm.Request{Model: "contract-model"})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Text != "ok" || response.Model != "contract-model" {
		t.Fatalf("Chat() response = %#v", response)
	}
	if _, err := providers.Get(""); err == nil || !strings.Contains(err.Error(), "provider name") {
		t.Fatalf("Get(empty) error = %v", err)
	}
	if _, err := providers.Get("missing"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("Get(missing) error = %v", err)
	}

	modelProviders, err := llm.NewProviderSet(llm.RegisterProvider(
		contractProvider{name: "beta"},
		llm.WithProviderName("Beta"),
		llm.WithProviderEnv("BETA_API_KEY"),
		llm.WithModels(llm.ModelInfo{
			ID:           "contract-model",
			Capabilities: llm.ModelCapabilities{Tools: true, Input: []string{"text"}, Output: []string{"text"}},
			Limits:       llm.ModelLimits{Context: 128_000, Output: 4_096},
		}),
		llm.WithDefaultModel("contract-model"),
	))
	if err != nil {
		t.Fatalf("NewProviderSet() error = %v", err)
	}
	info, err := modelProviders.ProviderInfo("beta")
	if err != nil {
		t.Fatalf("ProviderInfo(beta) error = %v", err)
	}
	if info.ID != "beta" || info.Name != "Beta" || info.Env[0] != "BETA_API_KEY" || info.DefaultModel != "contract-model" {
		t.Fatalf("ProviderInfo(beta) = %#v", info)
	}
	model, err := modelProviders.DefaultModel("beta")
	if err != nil {
		t.Fatalf("DefaultModel(beta) error = %v", err)
	}
	if model.ID != "contract-model" || model.Provider != "beta" || !model.Capabilities.Tools {
		t.Fatalf("DefaultModel(beta) = %#v", model)
	}
	refModel, err := modelProviders.Model("beta/contract-model")
	if err != nil {
		t.Fatalf("Model(beta/contract-model) error = %v", err)
	}
	if refModel.ID != "contract-model" || refModel.Provider != "beta" {
		t.Fatalf("Model(beta/contract-model) = %#v", refModel)
	}
	ref, err := llm.ParseModelRef("beta/contract-model")
	if err != nil {
		t.Fatalf("ParseModelRef() error = %v", err)
	}
	if ref.String() != "beta/contract-model" {
		t.Fatalf("ModelRef.String() = %q", ref.String())
	}
}

func TestHITLPublicHelperContracts(t *testing.T) {
	approved := hitl.Approve(map[string]string{"answer": "yes"})
	if approved.Decision != hitl.DecisionApproved {
		t.Fatalf("Approve() = %#v", approved)
	}
	approvedPayload, err := hitl.DecodePayload[map[string]string](approved)
	if err != nil {
		t.Fatalf("DecodePayload(approved) error = %v", err)
	}
	if approvedPayload["answer"] != "yes" {
		t.Fatalf("approved payload = %#v", approvedPayload)
	}

	rejected := hitl.Reject("no")
	if rejected.Decision != hitl.DecisionRejected || rejected.Reason != "no" || len(rejected.Payload) != 0 {
		t.Fatalf("Reject() = %#v", rejected)
	}

	provided := hitl.Provide("value")
	value, err := hitl.DecodePayload[string](provided)
	if err != nil {
		t.Fatalf("DecodePayload(provided) error = %v", err)
	}
	if provided.Decision != hitl.DecisionProvided || value != "value" {
		t.Fatalf("Provide() = %#v value=%q", provided, value)
	}

	request := hitl.NewRequest(hitl.RequestKindHumanInput, "need answer", map[string]string{"question": "Continue?"})
	if request.Kind != hitl.RequestKindHumanInput || request.Reason != "need answer" {
		t.Fatalf("NewRequest() = %#v", request)
	}
	var requestPayload map[string]string
	if err := json.Unmarshal(request.Payload, &requestPayload); err != nil {
		t.Fatalf("decode request payload: %v", err)
	}
	if requestPayload["question"] != "Continue?" {
		t.Fatalf("request payload = %#v", requestPayload)
	}
}

func TestToolPublicContracts(t *testing.T) {
	if schema := tool.JSONSchema(nil); string(schema) != `{"type":"object"}` {
		t.Fatalf("JSONSchema(nil) = %s", schema)
	}

	definition := tool.Define[apiToolInput, apiToolOutput]("lookup").
		Description("Lookup answer").
		InputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		})
	spec := definition.Spec()
	if spec.Name != "lookup" || spec.Description != "Lookup answer" || len(spec.InputSchema) == 0 {
		t.Fatalf("Spec() = %#v", spec)
	}
	spec.InputSchema[0] = '['
	if again := definition.Spec(); len(again.InputSchema) == 0 || again.InputSchema[0] == '[' {
		t.Fatalf("Spec() input schema was externally mutated: %s", again.InputSchema)
	}

	llmCalls := []llm.ToolCall{{
		ID:        "call_1",
		Name:      "lookup",
		Arguments: json.RawMessage(`{"query":"langquail"}`),
	}}
	calls := tool.FromLLMToolCalls(llmCalls)
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "lookup" {
		t.Fatalf("FromLLMToolCalls() = %#v", calls)
	}
	llmCalls[0].Arguments[0] = '['
	if string(calls[0].Arguments) != `{"query":"langquail"}` {
		t.Fatalf("tool call arguments were externally mutated: %s", calls[0].Arguments)
	}
	calls[0].Name = "changed"
	if llmCalls[0].Name != "lookup" {
		t.Fatalf("mutating tool calls changed source calls: %#v", llmCalls)
	}
}
