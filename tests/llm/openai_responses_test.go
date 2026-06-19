package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/superquail/langquail/llm"
	lqresponses "github.com/superquail/langquail/llm/openai/responses"
	"github.com/superquail/langquail/tests/testutil"
)

func TestOpenAIResponsesProviderMapsToolCallsAndInput(t *testing.T) {
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
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "none" || reasoning["summary"] != "auto" {
			handlerErrors.Failf(w, "reasoning = %#v", body["reasoning"])
			return
		}
		include, ok := body["include"].([]any)
		if !ok || len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			handlerErrors.Failf(w, "include = %#v", body["include"])
			return
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			handlerErrors.Failf(w, "tools = %#v", body["tools"])
			return
		}
		toolSpec := tools[0].(map[string]any)
		if toolSpec["type"] != "function" || toolSpec["name"] != "lookup" || toolSpec["description"] != "Lookup things" {
			handlerErrors.Failf(w, "tool spec = %#v", toolSpec)
			return
		}
		parameters := toolSpec["parameters"].(map[string]any)
		properties := parameters["properties"].(map[string]any)
		q := properties["q"].(map[string]any)
		if parameters["type"] != "object" || q["type"] != "string" {
			handlerErrors.Failf(w, "parameters = %#v", parameters)
			return
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 3 {
			handlerErrors.Failf(w, "input = %#v", body["input"])
			return
		}
		var sawCall, sawOutput bool
		for _, item := range input {
			object, ok := item.(map[string]any)
			if !ok {
				handlerErrors.Failf(w, "input item = %#v", item)
				return
			}
			if object["type"] == "function_call" && object["call_id"] == "call_prev" {
				if object["name"] != "lookup" || object["arguments"] != `{"q":"old"}` {
					handlerErrors.Failf(w, "function_call item = %#v", object)
					return
				}
				sawCall = true
			}
			if object["type"] == "function_call_output" && object["call_id"] == "call_prev" {
				if object["output"] != `{"result":"old result"}` {
					handlerErrors.Failf(w, "function_call_output item = %#v", object)
					return
				}
				sawOutput = true
			}
		}
		if !sawCall || !sawOutput {
			handlerErrors.Failf(w, "input did not preserve tool loop: %#v", input)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_1",
			"object":"response",
			"created_at":1,
			"model":"test-model",
			"output":[{
				"type":"function_call",
				"id":"fc_1",
				"call_id":"call_1",
				"name":"lookup",
				"arguments":"{\"q\":\"langquail\"}",
				"status":"completed"
			}],
			"status":"completed",
			"usage":{
				"input_tokens":5,
				"output_tokens":2,
				"total_tokens":7,
				"input_tokens_details":{"cached_tokens":3},
				"output_tokens_details":{}
			}
		}`))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model: "test-model",
		Messages: []llm.Message{
			llm.User("search"),
			llm.AssistantToolCalls("", []llm.ToolCall{{
				ID:        "call_prev",
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"old"}`),
			}}),
			llm.ToolResult("call_prev", `{"result":"old result"}`),
		},
		Tools: []llm.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		ToolChoice: llm.ToolChoiceAuto,
		Reasoning: &llm.ReasoningConfig{
			Effort:                  "none",
			Summary:                 "auto",
			IncludeEncryptedContent: true,
		},
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("Chat() error = %v", err)
	}
	handlerErrors.AssertNone()
	if gotPath != "/responses" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].ID != "call_1" || response.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if response.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if response.Usage.InputCachedTokens != 3 || response.Usage.InputUncachedTokens != 2 {
		t.Fatalf("cached usage = %#v", response.Usage)
	}
}

func TestOpenAIResponsesProviderMapsTextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_2",
			"object":"response",
			"created_at":1,
			"model":"test-model",
			"output":[{
				"type":"message",
				"id":"msg_1",
				"role":"assistant",
				"status":"completed",
				"content":[{"type":"output_text","text":"LangQuail is ready.","annotations":[]}]
			}],
			"status":"completed",
			"usage":{
				"input_tokens":8,
				"output_tokens":4,
				"total_tokens":12,
				"input_tokens_details":{},
				"output_tokens_details":{}
			}
		}`))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
	response, err := provider.Chat(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("answer")},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if response.Text != "LangQuail is ready." {
		t.Fatalf("text = %q", response.Text)
	}
	if response.Message.Role != llm.RoleAssistant {
		t.Fatalf("message = %#v", response.Message)
	}
}

func TestOpenAIResponsesProviderMapsImageInput(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 1 {
			handlerErrors.Failf(w, "input = %#v", body["input"])
			return
		}
		message := input[0].(map[string]any)
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
		if text["type"] != "input_text" || text["text"] != "describe this" {
			handlerErrors.Failf(w, "text part = %#v", text)
			return
		}
		image := content[1].(map[string]any)
		if image["type"] != "input_image" || image["image_url"] != "data:image/png;base64,AQI=" || image["detail"] != "auto" {
			handlerErrors.Failf(w, "image part = %#v", image)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_image",
			"object":"response",
			"created_at":1,
			"model":"test-model",
			"output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],
			"status":"completed",
			"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4,"input_tokens_details":{},"output_tokens_details":{}}
		}`))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
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
