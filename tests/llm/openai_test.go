package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/superquail/langquail/llm"
	lqopenai "github.com/superquail/langquail/llm/openai"
	"github.com/superquail/langquail/tests/testutil"
)

func TestOpenAIProviderMapsToolCalls(t *testing.T) {
	var gotPath string
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		if body["model"] != "test-model" {
			handlerErrors.Failf(w, "model = %v", body["model"])
			return
		}
		if body["tool_choice"] != "auto" {
			handlerErrors.Failf(w, "tool_choice = %#v", body["tool_choice"])
			return
		}
		messages, ok := body["messages"].([]any)
		if !ok || len(messages) != 5 {
			handlerErrors.Failf(w, "messages = %#v", body["messages"])
			return
		}
		if message := messages[0].(map[string]any); message["role"] != "system" || message["content"] != "system rules" {
			handlerErrors.Failf(w, "system message = %#v", messages[0])
			return
		}
		if message := messages[1].(map[string]any); message["role"] != "developer" || message["content"] != "developer rules" {
			handlerErrors.Failf(w, "developer message = %#v", messages[1])
			return
		}
		if message := messages[2].(map[string]any); message["role"] != "user" || message["content"] != "search" {
			handlerErrors.Failf(w, "user message = %#v", messages[2])
			return
		}
		assistant := messages[3].(map[string]any)
		if assistant["role"] != "assistant" || assistant["content"] != "previous lookup" {
			handlerErrors.Failf(w, "assistant message = %#v", assistant)
			return
		}
		assistantCalls, ok := assistant["tool_calls"].([]any)
		if !ok || len(assistantCalls) != 1 {
			handlerErrors.Failf(w, "assistant tool_calls = %#v", assistant["tool_calls"])
			return
		}
		assistantCall := assistantCalls[0].(map[string]any)
		if assistantCall["id"] != "call_prev" || assistantCall["type"] != "function" {
			handlerErrors.Failf(w, "assistant tool call = %#v", assistantCall)
			return
		}
		assistantFunction := assistantCall["function"].(map[string]any)
		if assistantFunction["name"] != "lookup" || assistantFunction["arguments"] != `{"q":"old"}` {
			handlerErrors.Failf(w, "assistant function = %#v", assistantFunction)
			return
		}
		toolMessage := messages[4].(map[string]any)
		if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "call_prev" || toolMessage["content"] != `{"answer":"old"}` {
			handlerErrors.Failf(w, "tool message = %#v", toolMessage)
			return
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			handlerErrors.Failf(w, "tools = %#v", body["tools"])
			return
		}
		toolSpec := tools[0].(map[string]any)
		if toolSpec["type"] != "function" {
			handlerErrors.Failf(w, "tool spec = %#v", toolSpec)
			return
		}
		function := toolSpec["function"].(map[string]any)
		if function["name"] != "lookup" || function["description"] != "Lookup things" {
			handlerErrors.Failf(w, "function = %#v", function)
			return
		}
		parameters := function["parameters"].(map[string]any)
		properties := parameters["properties"].(map[string]any)
		q := properties["q"].(map[string]any)
		if parameters["type"] != "object" || q["type"] != "string" {
			handlerErrors.Failf(w, "parameters = %#v", parameters)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_test",
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
						"function":{"name":"lookup","arguments":"{\"q\":\"langquail\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{
				"prompt_tokens":5,
				"completion_tokens":2,
				"total_tokens":7,
				"prompt_tokens_details":{"cached_tokens":3}
			}
		}`))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
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
			llm.ToolResult("call_prev", `{"answer":"old"}`),
		},
		Tools: []llm.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		ToolChoice: llm.ToolChoiceAuto,
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if gotPath != "/chat/completions" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Usage.InputCachedTokens != 3 || response.Usage.InputUncachedTokens != 2 {
		t.Fatalf("cached usage = %#v", response.Usage)
	}
}

func TestOpenAIProviderMapsImageInput(t *testing.T) {
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
		content, ok := message["content"].([]any)
		if !ok || len(content) != 2 {
			handlerErrors.Failf(w, "content = %#v", message["content"])
			return
		}
		text := content[0].(map[string]any)
		if text["type"] != "text" || text["text"] != "describe this" {
			handlerErrors.Failf(w, "text part = %#v", text)
			return
		}
		image := content[1].(map[string]any)
		imageURL := image["image_url"].(map[string]any)
		if image["type"] != "image_url" || imageURL["url"] != "data:image/png;base64,AQI=" {
			handlerErrors.Failf(w, "image part = %#v", image)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_image",
			"object":"chat.completion",
			"created":1,
			"model":"test-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}
		}`))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "test-model",
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
