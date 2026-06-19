package token_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/superquail/langquail/tests/testutil"
	"github.com/superquail/langquail/token"
	lqgemini "github.com/superquail/langquail/token/gemini"
)

func TestGeminiEstimatorCountPromptTokensMapsGenerateContentRequest(t *testing.T) {
	handlerErrors := testutil.NewHandlerErrors(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/test-model:countTokens" {
			handlerErrors.Failf(w, "path = %q", r.URL.Path)
			return
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			handlerErrors.Failf(w, "x-goog-api-key = %q", r.Header.Get("x-goog-api-key"))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			handlerErrors.Failf(w, "decode request: %v", err)
			return
		}
		generateRequest, ok := body["generateContentRequest"].(map[string]any)
		if !ok {
			handlerErrors.Failf(w, "generateContentRequest = %#v", body["generateContentRequest"])
			return
		}
		systemJSON, _ := json.Marshal(generateRequest["systemInstruction"])
		if !strings.Contains(string(systemJSON), "system rules") || !strings.Contains(string(systemJSON), "developer rules") {
			handlerErrors.Failf(w, "systemInstruction = %s", systemJSON)
			return
		}
		contentsJSON, _ := json.Marshal(generateRequest["contents"])
		for _, want := range []string{"search", "previous lookup", "inlineData", "image/png", "AQI=", "functionCall", "call_prev", "functionResponse", "old result"} {
			if !strings.Contains(string(contentsJSON), want) {
				handlerErrors.Failf(w, "contents = %s, want %q", contentsJSON, want)
				return
			}
		}
		toolsJSON, _ := json.Marshal(generateRequest["tools"])
		for _, want := range []string{"functionDeclarations", "lookup", "OBJECT", "STRING"} {
			if !strings.Contains(string(toolsJSON), want) {
				handlerErrors.Failf(w, "tools = %s, want %q", toolsJSON, want)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"totalTokens":37,"cachedContentTokenCount":4}`))
	}))
	defer server.Close()

	estimator := lqgemini.NewEstimator().APIKey("test-key").BaseURL(server.URL)
	estimate, err := estimator.CountPromptTokens(context.Background(), token.EstimateRequest{
		Provider: "gemini",
		Model:    "test-model",
		Messages: []token.Message{
			{Role: "system", Content: "system rules"},
			{Role: "developer", Content: "developer rules"},
			{Role: "user", Content: "search"},
			{Role: "assistant", Input: []token.InputPart{
				{Type: token.InputPartText, Text: "previous lookup"},
				{Type: token.InputPartImage, Image: &token.InputImage{MIMEType: "image/png", Data: []byte{1, 2}}},
			}, ToolCalls: []token.ToolCall{{
				ID:        "call_prev",
				Name:      "lookup",
				Arguments: json.RawMessage(`{"q":"old"}`),
			}}},
			{Role: "tool", ToolCallID: "call_prev", Content: `{"answer":"old result"}`},
		},
		Tools: []token.ToolSpec{{
			Name:        "lookup",
			Description: "Lookup things",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		ContextLimit:    100,
		MaxOutputTokens: 10,
	})
	if err != nil {
		handlerErrors.AssertNone()
		t.Fatalf("CountPromptTokens() error = %v", err)
	}
	handlerErrors.AssertNone()
	if estimate.Source != token.SourceGeminiAPI || !estimate.Estimated {
		t.Fatalf("estimate source/estimated = %s/%v", estimate.Source, estimate.Estimated)
	}
	if estimate.InputTokens != 37 || estimate.RemainingTokens != 53 {
		t.Fatalf("estimate = %#v", estimate)
	}
}
