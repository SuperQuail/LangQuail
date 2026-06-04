package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	lqopenai "github.com/superquail/langquail/llm/openai"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/tests/testutil"
	"github.com/superquail/langquail/tool"
	"github.com/superquail/langquail/trace"
)

type agentState struct {
	Messages []llm.Message
	Pending  []tool.Call
	Answer   string
}

func TestAgentLoopWithLLMToolAndMessages(t *testing.T) {
	calls := 0
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl_1",
				"object":"chat.completion",
				"created":1,
				"model":"test-model",
				"choices":[{
					"index":0,
					"message":{
						"role":"assistant",
						"content":"",
						"tool_calls":[{
							"id":"call_1",
							"type":"function",
							"function":{"name":"lookup","arguments":"{\"query\":\"langquail\"}"}
						}]
					},
					"finish_reason":"tool_calls"
				}],
				"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}
			}`))
			return
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode second request: %v", err)
			return
		}
		if len(body.Messages) != 3 {
			handlerErrors.Failf(w, "messages = %#v", body.Messages)
			return
		}
		if body.Messages[0]["role"] != "user" || body.Messages[0]["content"] != "Use lookup" {
			handlerErrors.Failf(w, "user message = %#v", body.Messages[0])
			return
		}
		assistant := body.Messages[1]
		if assistant["role"] != "assistant" {
			handlerErrors.Failf(w, "assistant message = %#v", assistant)
			return
		}
		toolCalls, ok := assistant["tool_calls"].([]any)
		if !ok || len(toolCalls) != 1 {
			handlerErrors.Failf(w, "assistant tool_calls = %#v", assistant["tool_calls"])
			return
		}
		toolCall, ok := toolCalls[0].(map[string]any)
		if !ok || toolCall["id"] != "call_1" || toolCall["type"] != "function" {
			handlerErrors.Failf(w, "tool call = %#v", toolCalls[0])
			return
		}
		function, ok := toolCall["function"].(map[string]any)
		if !ok || function["name"] != "lookup" || function["arguments"] != `{"query":"langquail"}` {
			handlerErrors.Failf(w, "tool call function = %#v", toolCall["function"])
			return
		}
		toolMessage := body.Messages[2]
		if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_1" {
			handlerErrors.Failf(w, "tool message = %#v", toolMessage)
			return
		}
		if content, ok := toolMessage["content"].(string); !ok || !strings.Contains(content, "LangQuail result for langquail") {
			handlerErrors.Failf(w, "tool message content = %#v", toolMessage["content"])
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_2",
			"object":"chat.completion",
			"created":2,
			"model":"test-model",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"LangQuail is ready."},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
		}`))
	}))
	defer server.Close()

	registry := tool.NewRegistry()
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Description("Lookup facts").
		InputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		}).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			return "LangQuail result for " + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	toolSpecs, err := registry.LLMSpecs("lookup")
	if err != nil {
		t.Fatalf("LLMSpecs() error = %v", err)
	}

	providers := llm.Providers(lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL))
	g := graph.NewStateGraph[agentState]("agent.loop")
	g.Node(llm.Node("decide", llm.NodeSpec[agentState]{
		Providers: providers,
		Provider:  "openai",
		Model:     "test-model",
		Tools:     toolSpecs,
		Messages: llm.MessagePolicy[agentState]{
			Read: func(ctx context.Context, state agentState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state agentState, write llm.MessageWrite) (agentState, error) {
				state.Messages = append(state.Messages, write.Response.Message)
				state.Pending = tool.FromLLMToolCalls(write.Response.ToolCalls)
				return state, nil
			},
		},
		Output: func(ctx context.Context, state agentState, response llm.Response) (graph.Command[agentState], error) {
			if len(response.ToolCalls) > 0 {
				return graph.UpdateAndGoto(state, "run_tool"), nil
			}
			state.Answer = response.Text
			return graph.Command[agentState]{Update: &state, End: true}, nil
		},
	}))
	g.Node(tool.Node("run_tool", tool.NodeSpec[agentState]{
		Registry: registry,
		Calls: func(ctx context.Context, state agentState) ([]tool.Call, error) {
			return state.Pending, nil
		},
		Output: func(ctx context.Context, state agentState, results []tool.Result) (graph.Command[agentState], error) {
			for _, result := range results {
				state.Messages = append(state.Messages, result.Message())
			}
			state.Pending = nil
			return graph.UpdateAndGoto(state, "decide"), nil
		},
	}))
	g.Start("decide")

	runner, err := lqruntime.NewRunner(g)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), agentState{Messages: []llm.Message{llm.User("Use lookup")}})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Invoke() error = %v", err)
	}
	handlerErrors.AssertNone()
	if result.State.Answer != "LangQuail is ready." {
		t.Fatalf("Answer = %q", result.State.Answer)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestAgentLoopWithStreamingLLMToolAndMessages(t *testing.T) {
	provider := &agentStreamProvider{name: "stream"}
	registry := tool.NewRegistry()
	toolRuns := 0
	if err := registry.Register(tool.Define[lookupInput, string]("lookup").
		Description("Lookup facts").
		InputSchema(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
		}).
		Execute(func(ctx context.Context, input lookupInput) (string, error) {
			toolRuns++
			return "Stream result for " + input.Query, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	toolSpecs, err := registry.LLMSpecs("lookup")
	if err != nil {
		t.Fatalf("LLMSpecs() error = %v", err)
	}

	g := graph.NewStateGraph[agentState]("agent.stream_loop")
	g.Node(llm.Node("decide", llm.NodeSpec[agentState]{
		Providers:  llm.Providers(provider),
		Provider:   "stream",
		Model:      "test-model",
		Stream:     true,
		Tools:      toolSpecs,
		ToolChoice: llm.ToolChoiceAuto,
		Messages: llm.MessagePolicy[agentState]{
			Read: func(ctx context.Context, state agentState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state agentState, write llm.MessageWrite) (agentState, error) {
				state.Messages = append(state.Messages, write.Response.Message)
				state.Pending = tool.FromLLMToolCalls(write.Response.ToolCalls)
				return state, nil
			},
		},
		Output: func(ctx context.Context, state agentState, response llm.Response) (graph.Command[agentState], error) {
			if len(response.ToolCalls) > 0 {
				return graph.UpdateAndGoto(state, "run_tool"), nil
			}
			state.Answer = response.Text
			return graph.Command[agentState]{Update: &state, End: true}, nil
		},
	}))
	g.Node(tool.Node("run_tool", tool.NodeSpec[agentState]{
		Registry: registry,
		Calls: func(ctx context.Context, state agentState) ([]tool.Call, error) {
			return state.Pending, nil
		},
		Output: func(ctx context.Context, state agentState, results []tool.Result) (graph.Command[agentState], error) {
			for _, result := range results {
				state.Messages = append(state.Messages, result.Message())
			}
			state.Pending = nil
			return graph.UpdateAndGoto(state, "decide"), nil
		},
	}))
	g.Start("decide")

	var deltas []string
	runner, err := lqruntime.NewRunner(g, lqruntime.WithEventHandler[agentState](func(ctx context.Context, event trace.Event) error {
		if event.Type != trace.EventLLMDelta {
			return nil
		}
		var chunk llm.StreamChunk
		if err := json.Unmarshal(event.Payload, &chunk); err != nil {
			return err
		}
		if chunk.Text != "" {
			deltas = append(deltas, chunk.Text)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), agentState{Messages: []llm.Message{llm.User("Use lookup")}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d", provider.calls)
	}
	if toolRuns != 1 {
		t.Fatalf("tool runs = %d", toolRuns)
	}
	if result.State.Answer != "LangQuail stream is ready." {
		t.Fatalf("Answer = %q", result.State.Answer)
	}
	if len(result.State.Messages) != 4 || result.State.Messages[2].Role != llm.RoleTool {
		t.Fatalf("messages = %#v", result.State.Messages)
	}
	if strings.Join(deltas, "") != "Need lookup.LangQuail stream is ready." {
		t.Fatalf("deltas = %#v", deltas)
	}
}

type lookupInput struct {
	Query string `json:"query"`
}

type agentStreamProvider struct {
	name  string
	calls int
}

func (p *agentStreamProvider) Name() string {
	return p.name
}

func (p *agentStreamProvider) Chat(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (p *agentStreamProvider) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	p.calls++
	if p.calls == 1 {
		call := llm.ToolCall{
			ID:        "call_1",
			Name:      "lookup",
			Arguments: json.RawMessage(`{"query":"langquail"}`),
		}
		if err := handler(ctx, llm.StreamChunk{Text: "Need lookup."}); err != nil {
			return llm.Response{}, err
		}
		if err := handler(ctx, llm.StreamChunk{ToolCall: &call}); err != nil {
			return llm.Response{}, err
		}
		if err := handler(ctx, llm.StreamChunk{Done: true}); err != nil {
			return llm.Response{}, err
		}
		return llm.Response{
			ID:        "resp_stream_tool",
			Model:     request.Model,
			Message:   llm.AssistantToolCalls("", []llm.ToolCall{call}),
			Text:      "Need lookup.",
			ToolCalls: []llm.ToolCall{call},
		}, nil
	}

	text := "LangQuail stream is ready."
	if err := handler(ctx, llm.StreamChunk{Text: text}); err != nil {
		return llm.Response{}, err
	}
	if err := handler(ctx, llm.StreamChunk{Done: true}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		ID:      "resp_stream_done",
		Model:   request.Model,
		Message: llm.Assistant(text),
		Text:    text,
	}, nil
}
