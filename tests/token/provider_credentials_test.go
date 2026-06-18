package token_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/token"
	lqanthropic "github.com/superquail/langquail/token/anthropic"
	lqgemini "github.com/superquail/langquail/token/gemini"
)

type credentialMode string

const (
	credentialNone     credentialMode = "none"
	credentialExplicit credentialMode = "explicit"
	credentialCustom   credentialMode = "custom"
	credentialMissing  credentialMode = "missing"
)

type tokenCredentialCase struct {
	name       string
	defaultEnv []string
	header     string
	response   string
	factory    func(baseURL, customEnv, missingEnv string, mode credentialMode) token.Estimator
}

func TestTokenEstimatorsDoNotFallbackToDefaultAPIKeyEnv(t *testing.T) {
	cases := []tokenCredentialCase{
		{
			name:       "anthropic",
			defaultEnv: []string{"ANTHROPIC_API_KEY"},
			header:     "x-api-key",
			response:   `{"input_tokens":7}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) token.Estimator {
				estimator := lqanthropic.NewEstimator().BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					estimator.APIKey("explicit-key")
				case credentialCustom:
					estimator.APIKeyFromEnv(customEnv)
				case credentialMissing:
					estimator.APIKeyFromEnv(missingEnv)
				}
				return estimator
			},
		},
		{
			name:       "gemini",
			defaultEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
			header:     "x-goog-api-key",
			response:   `{"totalTokens":9}`,
			factory: func(baseURL, customEnv, missingEnv string, mode credentialMode) token.Estimator {
				estimator := lqgemini.NewEstimator().BaseURL(baseURL)
				switch mode {
				case credentialExplicit:
					estimator.APIKey("explicit-key")
				case credentialCustom:
					estimator.APIKeyFromEnv(customEnv)
				case credentialMissing:
					estimator.APIKeyFromEnv(missingEnv)
				}
				return estimator
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runTokenCredentialCase(t, tc)
		})
	}
}

func runTokenCredentialCase(t *testing.T, tc tokenCredentialCase) {
	t.Helper()
	customEnv := "LANGQUAIL_TEST_TOKEN_" + strings.ToUpper(tc.name) + "_API_KEY"
	missingEnv := "LANGQUAIL_TEST_TOKEN_" + strings.ToUpper(tc.name) + "_MISSING_API_KEY"
	for _, name := range tc.defaultEnv {
		t.Setenv(name, "default-key")
	}
	t.Setenv(customEnv, "custom-key")
	t.Setenv(missingEnv, "")

	t.Run("missing_key_errors_before_request", func(t *testing.T) {
		var hits int
		server := tokenCredentialServer(t, tc, "", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialNone).CountPromptTokens(context.Background(), credentialTokenRequest())
		requireTokenErrorContains(t, err, "api key is required")
		if hits != 0 {
			t.Fatalf("server hits = %d, want 0", hits)
		}
	})

	t.Run("explicit_key_is_used", func(t *testing.T) {
		var hits int
		server := tokenCredentialServer(t, tc, "explicit-key", &hits)
		defer server.Close()
		estimate, err := tc.factory(server.URL, customEnv, missingEnv, credentialExplicit).CountPromptTokens(context.Background(), credentialTokenRequest())
		if err != nil {
			t.Fatalf("CountPromptTokens() error = %v", err)
		}
		if estimate.InputTokens <= 0 {
			t.Fatalf("estimate = %#v", estimate)
		}
		if hits != 1 {
			t.Fatalf("server hits = %d, want 1", hits)
		}
	})

	t.Run("custom_env_key_is_used", func(t *testing.T) {
		var hits int
		server := tokenCredentialServer(t, tc, "custom-key", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialCustom).CountPromptTokens(context.Background(), credentialTokenRequest())
		if err != nil {
			t.Fatalf("CountPromptTokens() error = %v", err)
		}
		if hits != 1 {
			t.Fatalf("server hits = %d, want 1", hits)
		}
	})

	t.Run("missing_custom_env_does_not_fallback", func(t *testing.T) {
		var hits int
		server := tokenCredentialServer(t, tc, "", &hits)
		defer server.Close()
		_, err := tc.factory(server.URL, customEnv, missingEnv, credentialMissing).CountPromptTokens(context.Background(), credentialTokenRequest())
		requireTokenErrorContains(t, err, "api key is required")
		if hits != 0 {
			t.Fatalf("server hits = %d, want 0", hits)
		}
	})
}

func tokenCredentialServer(t *testing.T, tc tokenCredentialCase, wantKey string, hits *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*hits)++
		if wantKey != "" && r.Header.Get(tc.header) != wantKey {
			t.Errorf("%s = %q, want %q", tc.header, r.Header.Get(tc.header), wantKey)
			http.Error(w, "wrong api key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tc.response))
	}))
}

func credentialTokenRequest() token.EstimateRequest {
	return token.EstimateRequest{
		Model:    "test-model",
		Messages: []token.Message{{Role: "user", Content: "hello"}},
	}
}

func requireTokenErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
