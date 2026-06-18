package llm_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/llm"
	lqanthropic "github.com/superquail/langquail/llm/anthropic"
	lqgemini "github.com/superquail/langquail/llm/gemini"
	lqopenai "github.com/superquail/langquail/llm/openai"
	lqresponses "github.com/superquail/langquail/llm/openai/responses"
)

type credentialMode string

const (
	credentialNone     credentialMode = "none"
	credentialExplicit credentialMode = "explicit"
	credentialCustom   credentialMode = "custom"
	credentialMissing  credentialMode = "missing"
)

type llmCredentialCase struct {
	name        string
	defaultEnv  []string
	header      string
	headerValue func(string) string
	response    string
	factory     func(baseURL, customEnv, missingEnv string, mode credentialMode) llm.Provider
}

func TestLLMProvidersDoNotFallbackToDefaultAPIKeyEnv(t *testing.T) {
	cases := []llmCredentialCase{
		{
			name:        "openai",
			defaultEnv:  []string{"OPENAI_API_KEY"},
			header:      "Authorization",
			headerValue: func(key string) string { return "Bearer " + key },
			response: `{
				"id":"chatcmpl_credentials",
				"object":"chat.completion",
				"created":1,
				"model":"test-model",
				"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
			}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) llm.Provider {
				provider := lqopenai.Provider("openai").BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					provider.APIKey("explicit-key")
				case credentialCustom:
					provider.APIKeyFromEnv(customEnv)
				case credentialMissing:
					provider.APIKeyFromEnv(missingEnv)
				}
				return provider
			},
		},
		{
			name:        "openai_responses",
			defaultEnv:  []string{"OPENAI_API_KEY"},
			header:      "Authorization",
			headerValue: func(key string) string { return "Bearer " + key },
			response: `{
				"id":"resp_credentials",
				"object":"response",
				"created_at":1,
				"model":"test-model",
				"output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],
				"status":"completed",
				"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{},"output_tokens_details":{}}
			}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) llm.Provider {
				provider := lqresponses.Provider("openai.responses").BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					provider.APIKey("explicit-key")
				case credentialCustom:
					provider.APIKeyFromEnv(customEnv)
				case credentialMissing:
					provider.APIKeyFromEnv(missingEnv)
				}
				return provider
			},
		},
		{
			name:        "anthropic",
			defaultEnv:  []string{"ANTHROPIC_API_KEY"},
			header:      "x-api-key",
			headerValue: func(key string) string { return key },
			response: `{
				"id":"msg_credentials",
				"type":"message",
				"role":"assistant",
				"model":"claude-test",
				"content":[{"type":"text","text":"ok"}],
				"stop_reason":"end_turn",
				"stop_sequence":"",
				"usage":{"input_tokens":1,"output_tokens":1}
			}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) llm.Provider {
				provider := lqanthropic.Provider("claude").BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					provider.APIKey("explicit-key")
				case credentialCustom:
					provider.APIKeyFromEnv(customEnv)
				case credentialMissing:
					provider.APIKeyFromEnv(missingEnv)
				}
				return provider
			},
		},
		{
			name:        "gemini",
			defaultEnv:  []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
			header:      "x-goog-api-key",
			headerValue: func(key string) string { return key },
			response: `{
				"responseId":"gemini_credentials",
				"modelVersion":"test-model",
				"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],
				"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}
			}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) llm.Provider {
				provider := lqgemini.Provider("gemini").BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					provider.APIKey("explicit-key")
				case credentialCustom:
					provider.APIKeyFromEnv(customEnv)
				case credentialMissing:
					provider.APIKeyFromEnv(missingEnv)
				}
				return provider
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runLLMCredentialCase(t, tc)
		})
	}
}

func runLLMCredentialCase(t *testing.T, tc llmCredentialCase) {
	t.Helper()
	customEnv := "LANGQUAIL_TEST_" + strings.ToUpper(tc.name) + "_API_KEY"
	missingEnv := "LANGQUAIL_TEST_" + strings.ToUpper(tc.name) + "_MISSING_API_KEY"
	for _, name := range tc.defaultEnv {
		t.Setenv(name, "default-key")
	}
	t.Setenv(customEnv, "custom-key")
	t.Setenv(missingEnv, "")

	t.Run("missing_key_errors_before_request", func(t *testing.T) {
		var hits int
		server := credentialServer(t, tc, "", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialNone).Chat(context.Background(), credentialLLMRequest())
		requireErrorContains(t, err, "api key is required")
		if hits != 0 {
			t.Fatalf("server hits = %d, want 0", hits)
		}
	})

	t.Run("explicit_key_is_used", func(t *testing.T) {
		var hits int
		server := credentialServer(t, tc, "explicit-key", &hits)
		defer server.Close()
		response, err := tc.factory(server.URL, customEnv, missingEnv, credentialExplicit).Chat(context.Background(), credentialLLMRequest())
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		if response.Text != "ok" {
			t.Fatalf("response text = %q", response.Text)
		}
		if hits != 1 {
			t.Fatalf("server hits = %d, want 1", hits)
		}
	})

	t.Run("custom_env_key_is_used", func(t *testing.T) {
		var hits int
		server := credentialServer(t, tc, "custom-key", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialCustom).Chat(context.Background(), credentialLLMRequest())
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		if hits != 1 {
			t.Fatalf("server hits = %d, want 1", hits)
		}
	})

	t.Run("missing_custom_env_does_not_fallback", func(t *testing.T) {
		var hits int
		server := credentialServer(t, tc, "", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialMissing).Chat(context.Background(), credentialLLMRequest())
		requireErrorContains(t, err, "api key is required")
		if hits != 0 {
			t.Fatalf("server hits = %d, want 0", hits)
		}
	})
}

func credentialServer(t *testing.T, tc llmCredentialCase, wantKey string, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*hits)++
		if wantKey != "" {
			if got, want := r.Header.Get(tc.header), tc.headerValue(wantKey); got != want {
				t.Errorf("%s = %q, want %q", tc.header, got, want)
				http.Error(w, "wrong api key", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tc.response))
	}))
}

func credentialLLMRequest() llm.Request {
	return llm.Request{
		Model:    "test-model",
		Messages: []llm.Message{llm.User("hello")},
	}
}
