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
	lqopenai "github.com/superquail/langquail/llm/openai"
	lqresponses "github.com/superquail/langquail/llm/openai/responses"
	"github.com/superquail/langquail/tests/testutil"
)

func TestOpenAIProviderChatStreamMapsTextToolCallsAndUsage(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			handlerErrors.Failf(w, "path = %q", r.URL.Path)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode body: %v", err)
			return
		}
		if body["stream"] != true {
			handlerErrors.Failf(w, "stream = %#v", body["stream"])
			return
		}
		if body["reasoning_effort"] != "none" {
			handlerErrors.Failf(w, "reasoning_effort = %#v", body["reasoning_effort"])
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Checking "},"finish_reason":null}],"usage":null}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"workspace."},"finish_reason":null}],"usage":null}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"file_read","arguments":""}}]},"finish_reason":null}],"usage":null}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"go.mod\"}"}}]},"finish_reason":null}],"usage":null}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":null}`,
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8,"prompt_tokens_details":{"cached_tokens":2}}}`,
			`data: [DONE]`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	var deltas []string
	var sawDone bool
	response, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("read")},
		Tools: []llm.ToolSpec{{
			Name:        "file_read",
			Description: "Read file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		ToolChoice: llm.ToolChoiceAuto,
		Reasoning:  &llm.ReasoningConfig{Effort: "none"},
	}, func(ctx context.Context, chunk llm.StreamChunk) error {
		if chunk.Text != "" {
			deltas = append(deltas, chunk.Text)
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
	if strings.Join(deltas, "") != "Checking workspace." {
		t.Fatalf("deltas = %#v", deltas)
	}
	if response.Text != "Checking workspace." {
		t.Fatalf("text = %q", response.Text)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "file_read" {
		t.Fatalf("tool calls = %#v", response.ToolCalls)
	}
	if string(response.ToolCalls[0].Arguments) != `{"path":"go.mod"}` {
		t.Fatalf("arguments = %s", response.ToolCalls[0].Arguments)
	}
	if response.Usage.TotalTokens != 8 || response.Usage.InputCachedTokens != 2 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if !sawDone {
		t.Fatalf("stream did not emit done chunk")
	}
}

func TestOpenAIResponsesProviderChatStreamMapsTextAndUsage(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			handlerErrors.Failf(w, "path = %q", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.reasoning_summary_text.delta","sequence_number":1,"delta":"Thinking"}`,
			`data: {"type":"response.output_text.delta","sequence_number":1,"delta":"Hi "}`,
			`data: {"type":"response.output_text.delta","sequence_number":2,"delta":"there"}`,
			`data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_1","object":"response","created_at":1,"model":"test-model","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hi there","annotations":[]}]}],"status":"completed","usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4,"input_tokens_details":{},"output_tokens_details":{}}}}`,
			`data: [DONE]`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
	var text strings.Builder
	var thinking strings.Builder
	response, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, func(ctx context.Context, chunk llm.StreamChunk) error {
		text.WriteString(chunk.Text)
		thinking.WriteString(chunk.Thinking)
		return nil
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("ChatStream() error = %v", err)
	}
	handlerErrors.AssertNone()
	if text.String() != "Hi there" || response.Text != "Hi there" {
		t.Fatalf("text chunks = %q response = %q", text.String(), response.Text)
	}
	if thinking.String() != "Thinking" {
		t.Fatalf("thinking = %q", thinking.String())
	}
	if response.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestAnthropicProviderChatStreamMapsTextAndUsage(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			handlerErrors.Failf(w, "path = %q", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":0}}}`,
			"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}`,
			"event: message_delta\n" +
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`,
			"event: message_stop\n" +
				`data: {"type":"message_stop"}`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	var text strings.Builder
	response, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "claude-test",
		Messages: []llm.Message{llm.User("hello")},
	}, func(ctx context.Context, chunk llm.StreamChunk) error {
		text.WriteString(chunk.Text)
		return nil
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("ChatStream() error = %v", err)
	}
	handlerErrors.AssertNone()
	if text.String() != "Hello" || response.Text != "Hello" {
		t.Fatalf("text chunks = %q response = %q", text.String(), response.Text)
	}
	if response.Usage.TotalTokens != 5 || response.StopReason != "end_turn" {
		t.Fatalf("response = %#v", response)
	}
}
