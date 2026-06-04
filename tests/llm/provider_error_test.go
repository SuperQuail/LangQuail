package llm_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
	lqanthropic "github.com/superquail/langquail/llm/anthropic"
	lqopenai "github.com/superquail/langquail/llm/openai"
	lqresponses "github.com/superquail/langquail/llm/openai/responses"
)

func TestOpenAIProviderChatReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream failed"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.Chat(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	})
	requireErrorContains(t, err, "upstream failed")
}

func TestOpenAIProviderChatRejectsEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_empty",
			"object":"chat.completion",
			"created":1,
			"model":"test-model",
			"choices":[]
		}`))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.Chat(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	})
	if err == nil || !strings.Contains(err.Error(), "empty chat completion") {
		t.Fatalf("Chat() error = %v, want empty chat completion", err)
	}
}

func TestOpenAIProviderChatStreamPropagatesHandlerError(t *testing.T) {
	wantErr := errors.New("handler stopped")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chatcmpl_stream","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}],"usage":null}`,
			`data: [DONE]`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, func(context.Context, llm.StreamChunk) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ChatStream() error = %v, want %v", err, wantErr)
	}
}

func TestOpenAIProviderChatStreamReturnsMalformedChunkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {not-json}\n\n"))
	}))
	defer server.Close()

	provider := lqopenai.Provider("openai").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, nil)
	requireErrorContains(t, err, "invalid character")
}

func TestOpenAIResponsesProviderStreamFailedReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.failed","sequence_number":1}`,
			`data: [DONE]`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "response.failed") {
		t.Fatalf("ChatStream() error = %v, want response.failed", err)
	}
}

func TestOpenAIResponsesProviderStreamWithoutCompletedReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.output_text.delta","sequence_number":1,"delta":"Hi"}`,
			`data: [DONE]`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqresponses.Provider("openai.responses").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "stream completed without response") {
		t.Fatalf("ChatStream() error = %v, want missing final response", err)
	}
}

func TestAnthropicProviderChatReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"upstream failed"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.Chat(context.Background(), llm.Request{
		Model:    "claude-test",
		Messages: []llm.Message{llm.User("hello")},
	})
	requireErrorContains(t, err, "upstream failed")
}

func TestAnthropicProviderChatStreamPropagatesHandlerError(t *testing.T) {
	wantErr := errors.New("handler stopped")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
			"event: content_block_start\n" +
				`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
			"event: content_block_stop\n" +
				`data: {"type":"content_block_stop","index":0}`,
			"event: message_stop\n" +
				`data: {"type":"message_stop"}`,
			``,
		}, "\n\n")))
	}))
	defer server.Close()

	provider := lqanthropic.Provider("claude").APIKey("test-key").BaseURL(server.URL)
	_, err := provider.ChatStream(context.Background(), llm.Request{
		Model:    "claude-test",
		Messages: []llm.Message{llm.User("hello")},
	}, func(context.Context, llm.StreamChunk) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ChatStream() error = %v, want %v", err, wantErr)
	}
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
