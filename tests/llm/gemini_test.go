package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
	lqgemini "github.com/superquail/langquail/llm/gemini"
	"github.com/superquail/langquail/tests/testutil"
)

func TestGeminiProviderMapsMessagesToolsAndUsage(t *testing.T) {
	var gotPath string
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("x-goog-api-key") != "test-key" {
			handlerErrors.Failf(w, "x-goog-api-key = %q", r.Header.Get("x-goog-api-key"))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		assertGeminiGenerateRequest(t, handlerErrors, w, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"responseId":"gemini_resp",
			"modelVersion":"models/test-model-version",
			"candidates":[{
				"content":{"role":"model","parts":[
					{"text":"Found "},
					{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"langquail"}}}
				]},
				"finishReason":"STOP"
			}],
			"usageMetadata":{
				"promptTokenCount":10,
				"cachedContentTokenCount":4,
				"candidatesTokenCount":3,
				"thoughtsTokenCount":2,
				"totalTokenCount":15
			}
		}`))
	}))
	defer server.Close()

	temperature := 0.2
	provider := lqgemini.Provider("gemini").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			llm.System("system rules"),
			llm.Developer("developer rules"),
			llm.User("search"),
			llm.AssistantToolCalls("previous lookup", []llm.ToolCall{{
				ID:        "call_prev",
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"old"}`),
			}}),
			llm.ToolResult("call_prev", `{"answer":"old result"}`),
		},
		Tools: []llm.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`),
		}},
		ToolChoice:  llm.ToolChoiceAuto,
		MaxTokens:   64,
		Temperature: &temperature,
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if gotPath != "/v1beta/models/test-model:generateContent" {
		t.Fatalf("path = %q", gotPath)
	}
	if response.ID != "gemini_resp" || response.Model != "models/test-model-version" || response.Text != "Found " {
		t.Fatalf("response = %#v", response)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call_1" || response.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if string(response.ToolCalls[0].Arguments) != `{"q":"langquail"}` {
		t.Fatalf("arguments = %s", response.ToolCalls[0].Arguments)
	}
	if response.Usage.InputTokens != 10 || response.Usage.InputCachedTokens != 4 || response.Usage.InputUncachedTokens != 6 {
		t.Fatalf("input usage = %#v", response.Usage)
	}
	if response.Usage.OutputTokens != 3 || response.Usage.ReasoningTokens != 2 || response.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.StopReason != "STOP" || len(response.Raw) == 0 {
		t.Fatalf("response metadata = %#v raw=%s", response, response.Raw)
	}
}

func assertGeminiGenerateRequest(t *testing.T, handlerErrors *testutil.HandlerErrors, w http.ResponseWriter, body map[string]any) {
	t.Helper()
	system, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		handlerErrors.Failf(w, "systemInstruction = %#v", body["systemInstruction"])
		return
	}
	systemJSON, _ := json.Marshal(system)
	if !strings.Contains(string(systemJSON), "system rules") || !strings.Contains(string(systemJSON), "developer rules") {
		handlerErrors.Failf(w, "systemInstruction = %s", systemJSON)
		return
	}

	generationConfig, ok := body["generationConfig"].(map[string]any)
	if !ok || generationConfig["maxOutputTokens"] != float64(64) || generationConfig["temperature"] != 0.2 {
		handlerErrors.Failf(w, "generationConfig = %#v", body["generationConfig"])
		return
	}
	contents, ok := body["contents"].([]any)
	if !ok || len(contents) != 3 {
		handlerErrors.Failf(w, "contents = %#v", body["contents"])
		return
	}
	user := contents[0].(map[string]any)
	if user["role"] != "user" || !contentContainsText(user, "search") {
		handlerErrors.Failf(w, "user content = %#v", user)
		return
	}
	assistant := contents[1].(map[string]any)
	if assistant["role"] != "model" || !contentContainsText(assistant, "previous lookup") {
		handlerErrors.Failf(w, "assistant content = %#v", assistant)
		return
	}
	assistantJSON, _ := json.Marshal(assistant)
	for _, want := range []string{"functionCall", "call_prev", "lookup", "old"} {
		if !strings.Contains(string(assistantJSON), want) {
			handlerErrors.Failf(w, "assistant content = %s, want %q", assistantJSON, want)
			return
		}
	}
	toolResult := contents[2].(map[string]any)
	toolResultJSON, _ := json.Marshal(toolResult)
	for _, want := range []string{"functionResponse", "call_prev", "lookup", "old result"} {
		if !strings.Contains(string(toolResultJSON), want) {
			handlerErrors.Failf(w, "tool result = %s, want %q", toolResultJSON, want)
			return
		}
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		handlerErrors.Failf(w, "tools = %#v", body["tools"])
		return
	}
	toolsJSON, _ := json.Marshal(tools)
	for _, want := range []string{"functionDeclarations", "lookup", "Lookup things", "OBJECT", "STRING"} {
		if !strings.Contains(string(toolsJSON), want) {
			handlerErrors.Failf(w, "tools = %s, want %q", toolsJSON, want)
			return
		}
	}
	toolConfig, ok := body["toolConfig"].(map[string]any)
	if !ok {
		handlerErrors.Failf(w, "toolConfig = %#v", body["toolConfig"])
		return
	}
	toolConfigJSON, _ := json.Marshal(toolConfig)
	if !strings.Contains(string(toolConfigJSON), `"mode":"AUTO"`) {
		handlerErrors.Failf(w, "toolConfig = %s", toolConfigJSON)
		return
	}
}

func contentContainsText(content map[string]any, want string) bool {
	parts, ok := content["parts"].([]any)
	if !ok {
		return false
	}
	for _, part := range parts {
		partMap, ok := part.(map[string]any)
		if ok && partMap["text"] == want {
			return true
		}
	}
	return false
}

func TestGeminiProviderChatStreamMapsTextThinkingToolCallsAndUsage(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/test-model:streamGenerateContent" {
			handlerErrors.Failf(w, "path = %q", r.URL.Path)
			return
		}
		if r.URL.Query().Get("alt") != "sse" {
			handlerErrors.Failf(w, "alt = %q", r.URL.Query().Get("alt"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"responseId":"stream_1","modelVersion":"models/test-model-version","candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]},"finishReason":""}]}`,
			`data: {"responseId":"stream_1","modelVersion":"models/test-model-version","candidates":[{"content":{"role":"model","parts":[{"text":"thinking","thought":true}]},"finishReason":""}]}`,
			`data: {"responseId":"stream_1","modelVersion":"models/test-model-version","candidates":[{"content":{"role":"model","parts":[{"text":"world"},{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"stream"}}}]},"finishReason":"STOP"}]}`,
			`data: {"responseId":"stream_1","modelVersion":"models/test-model-version","usageMetadata":{"promptTokenCount":5,"cachedContentTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":8}}`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqgemini.Provider("gemini").APIKey("test-key").BaseURL(server.URL)
	var text strings.Builder
	var thinking strings.Builder
	var calls []llm.ToolCall
	var usage llm.Usage
	var sawDone bool
	response, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, func(ctx context.Context, chunk llm.StreamChunk) error {
		text.WriteString(chunk.Text)
		thinking.WriteString(chunk.Thinking)
		if chunk.ToolCall != nil {
			calls = append(calls, *chunk.ToolCall)
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if chunk.Done {
			sawDone = true
		}
		return nil
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("ChatStream() error = %v", err)
	}
	handlerErrors.AssertNone()
	if text.String() != "Hello world" || response.Text != "Hello world" {
		t.Fatalf("text chunks = %q response = %q", text.String(), response.Text)
	}
	if thinking.String() != "thinking" {
		t.Fatalf("thinking = %q", thinking.String())
	}
	if len(calls) != 1 || calls[0].Name != "lookup" || string(calls[0].Arguments) != `{"q":"stream"}` {
		t.Fatalf("stream calls = %#v", calls)
	}
	if response.Usage.TotalTokens != 8 || usage.InputCachedTokens != 2 {
		t.Fatalf("usage chunk = %#v response = %#v", usage, response.Usage)
	}
	if response.StopReason != "STOP" || !sawDone {
		t.Fatalf("stream response = %#v sawDone=%v", response, sawDone)
	}
}
