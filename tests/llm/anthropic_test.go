package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
	lqanthropic "github.com/superquail/langquail/llm/anthropic"
	"github.com/superquail/langquail/tests/testutil"
)

func TestAnthropicProviderMapsToolUse(t *testing.T) {
	var gotPath string
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		if body["model"] != "claude-test" {
			handlerErrors.Failf(w, "model = %v", body["model"])
			return
		}
		systemJSON, err := json.Marshal(body["system"])
		if err != nil {
			handlerErrors.Failf(w, "marshal system: %v", err)
			return
		}
		if !strings.Contains(string(systemJSON), "system rules") || !strings.Contains(string(systemJSON), "developer rules") {
			handlerErrors.Failf(w, "system = %#v", body["system"])
			return
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 3 {
			handlerErrors.Failf(w, "messages = %#v", body["messages"])
			return
		}
		user := messages[0].(map[string]any)
		if user["role"] != "user" {
			handlerErrors.Failf(w, "user message = %#v", user)
			return
		}
		userContent, err := json.Marshal(user["content"])
		if err != nil {
			handlerErrors.Failf(w, "marshal user content: %v", err)
			return
		}
		if !strings.Contains(string(userContent), "search") {
			handlerErrors.Failf(w, "user content = %#v", user["content"])
			return
		}
		assistant := messages[1].(map[string]any)
		if assistant["role"] != "assistant" {
			handlerErrors.Failf(w, "assistant message = %#v", assistant)
			return
		}
		assistantContent, err := json.Marshal(assistant["content"])
		if err != nil {
			handlerErrors.Failf(w, "marshal assistant content: %v", err)
			return
		}
		for _, want := range []string{"previous lookup", "toolu_prev", "lookup", "old"} {
			if !strings.Contains(string(assistantContent), want) {
				handlerErrors.Failf(w, "assistant content = %s, want %q", assistantContent, want)
				return
			}
		}
		toolResult := messages[2].(map[string]any)
		if toolResult["role"] != "user" {
			handlerErrors.Failf(w, "tool result message = %#v", toolResult)
			return
		}
		toolResultContent, err := json.Marshal(toolResult["content"])
		if err != nil {
			handlerErrors.Failf(w, "marshal tool result content: %v", err)
			return
		}
		for _, want := range []string{"tool_result", "toolu_prev", "old result"} {
			if !strings.Contains(string(toolResultContent), want) {
				handlerErrors.Failf(w, "tool result content = %s, want %q", toolResultContent, want)
				return
			}
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			handlerErrors.Failf(w, "tools = %#v", body["tools"])
			return
		}
		toolSpec := tools[0].(map[string]any)
		if toolSpec["name"] != "lookup" || toolSpec["description"] != "Lookup things" {
			handlerErrors.Failf(w, "tool spec = %#v", toolSpec)
			return
		}
		inputSchema := toolSpec["input_schema"].(map[string]any)
		properties := inputSchema["properties"].(map[string]any)
		q := properties["q"].(map[string]any)
		if inputSchema["type"] != "object" || q["type"] != "string" {
			handlerErrors.Failf(w, "input_schema = %#v", inputSchema)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_test",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{
				"type":"tool_use",
				"id":"toolu_1",
				"name":"lookup",
				"input":{"q":"langquail"}
			}],
			"stop_reason":"tool_use",
			"stop_sequence":"",
			"usage":{
				"input_tokens":6,
				"output_tokens":3,
				"cache_creation_input_tokens":2,
				"cache_read_input_tokens":4,
				"cache_creation":{"ephemeral_1h_input_tokens":1,"ephemeral_5m_input_tokens":1}
			}
		}`))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{
			llm.System("system rules"),
			llm.Developer("developer rules"),
			llm.User("search"),
			llm.AssistantToolCalls("previous lookup", []llm.ToolCall{{
				ID:        "toolu_prev",
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"old"}`),
			}}),
			llm.ToolResult("toolu_prev", `{"answer":"old result"}`),
		},
		Tools: []llm.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Usage.InputUncachedTokens != 6 || response.Usage.InputCacheCreationTokens != 2 || response.Usage.InputCachedTokens != 4 {
		t.Fatalf("cached usage = %#v", response.Usage)
	}
}

func TestAnthropicProviderMapsReasoningEffort(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beta := r.Header.Get("anthropic-beta"); beta != "" {
			handlerErrors.Failf(w, "anthropic-beta = %q", beta)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		thinking, ok := body["thinking"].(map[string]any)
		if !ok || thinking["type"] != "adaptive" {
			handlerErrors.Failf(w, "thinking = %#v", body["thinking"])
			return
		}
		if thinking["display"] != "omitted" {
			handlerErrors.Failf(w, "thinking display = %#v", thinking["display"])
			return
		}
		if _, exists := thinking["budget_tokens"]; exists {
			handlerErrors.Failf(w, "thinking includes budget_tokens: %#v", thinking)
			return
		}
		outputConfig, ok := body["output_config"].(map[string]any)
		if !ok || outputConfig["effort"] != "high" {
			handlerErrors.Failf(w, "output_config = %#v", body["output_config"])
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_effort",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":"",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	enable := true
	response, err := provider.Chat(context.Background(), llm.Request{
		Model:     "claude-test",
		Messages:  []llm.Message{llm.User("think")},
		Reasoning: &llm.ReasoningConfig{Enable: &enable, Effort: "high", Display: "omitted"},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if response.Text != "ok" {
		t.Fatalf("text = %q", response.Text)
	}
}

func TestAnthropicProviderMapsImageInput(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 1 {
			handlerErrors.Failf(w, "messages = %#v", body["messages"])
			return
		}
		message := messages[0].(map[string]any)
		if message["role"] != "user" {
			handlerErrors.Failf(w, "message = %#v", message)
			return
		}
		contentJSON, _ := json.Marshal(message["content"])
		for _, want := range []string{"describe this", "image", "base64", "image/png", "AQI="} {
			if !strings.Contains(string(contentJSON), want) {
				handlerErrors.Failf(w, "content = %s, want %q", contentJSON, want)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_image",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{llm.UserInput(
			llm.InputText("describe this"),
			llm.InputImageData("image/png", []byte{1, 2}),
		)},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if response.Text != "ok" {
		t.Fatalf("text = %q", response.Text)
	}
}

func TestAnthropicProviderMapsAssistantImageInput(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 2 {
			handlerErrors.Failf(w, "messages = %#v", body["messages"])
			return
		}
		assistant := messages[1].(map[string]any)
		if assistant["role"] != "assistant" {
			handlerErrors.Failf(w, "assistant message = %#v", assistant)
			return
		}
		contentJSON, _ := json.Marshal(assistant["content"])
		for _, want := range []string{"generated image", "image", "base64", "image/png", "AwQ="} {
			if !strings.Contains(string(contentJSON), want) {
				handlerErrors.Failf(w, "assistant content = %s, want %q", contentJSON, want)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_assistant_image",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{
			llm.User("start"),
			llm.AssistantInput(
				llm.InputText("generated image"),
				llm.InputImageData("image/png", []byte{3, 4}),
			),
		},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if response.Text != "ok" {
		t.Fatalf("text = %q", response.Text)
	}
}

func TestAnthropicProviderMapsDisabledThinkingWithEffort(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		thinking, ok := body["thinking"].(map[string]any)
		if !ok || thinking["type"] != "disabled" {
			handlerErrors.Failf(w, "thinking = %#v", body["thinking"])
			return
		}
		outputConfig, ok := body["output_config"].(map[string]any)
		if !ok || outputConfig["effort"] != "low" {
			handlerErrors.Failf(w, "output_config = %#v", body["output_config"])
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"msg_disabled_effort",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"stop_sequence":"",
			"usage":{"input_tokens":1,"output_tokens":1}
		}`))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	enable := false
	_, err := provider.Chat(context.Background(), llm.Request{
		Model:     "claude-test",
		Messages:  []llm.Message{llm.User("think less")},
		Reasoning: &llm.ReasoningConfig{Enable: &enable, Effort: "low"},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
}
