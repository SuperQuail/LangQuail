package token_test

import (
	"context"
	"testing"

	"github.com/superquail/langquail/token"
)

func TestTiktokenEstimatorCountsPromptTokens(t *testing.T) {
	estimator := token.NewTiktokenEstimator()
	estimate, err := estimator.CountPromptTokens(context.Background(), token.EstimateRequest{
		Provider: "openai",
		Model:    "gpt-4o-mini",
		Messages: []token.Message{
			{Role: "system", Content: "Follow the release policy."},
			{Role: "user", Content: "Plan a safe release."},
		},
		Tools: []token.ToolSpec{{
			Name:        "repo.inspect",
			Description: "Inspect repository changes",
			InputSchema: []byte(`{"type":"object","properties":{"repo":{"type":"string"}}}`),
		}},
		ContextLimit:    128000,
		MaxOutputTokens: 1000,
	})
	if err != nil {
		t.Fatalf("CountPromptTokens() error = %v", err)
	}
	if estimate.Source != token.SourceTiktoken || !estimate.Estimated {
		t.Fatalf("estimate source/estimated = %s/%v", estimate.Source, estimate.Estimated)
	}
	if estimate.InputTokens <= 0 || estimate.MessageTokens <= 0 || estimate.ToolSchemaTokens <= 0 {
		t.Fatalf("estimate token counts = %#v", estimate)
	}
	if estimate.RemainingTokens <= 0 {
		t.Fatalf("RemainingTokens = %d", estimate.RemainingTokens)
	}
}

func TestTiktokenEstimatorDoesNotTokenizeImageBytes(t *testing.T) {
	estimator := token.NewTiktokenEstimator()
	small, err := estimator.CountPromptTokens(context.Background(), token.EstimateRequest{
		Model: "gpt-4o-mini",
		Messages: []token.Message{{
			Role: "user",
			Input: []token.InputPart{
				{Type: token.InputPartText, Text: "describe this"},
				{Type: token.InputPartImage, Image: &token.InputImage{MIMEType: "image/png", Data: []byte{1}}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CountPromptTokens(small) error = %v", err)
	}
	largeData := make([]byte, 4096)
	large, err := estimator.CountPromptTokens(context.Background(), token.EstimateRequest{
		Model: "gpt-4o-mini",
		Messages: []token.Message{{
			Role: "user",
			Input: []token.InputPart{
				{Type: token.InputPartText, Text: "describe this"},
				{Type: token.InputPartImage, Image: &token.InputImage{MIMEType: "image/png", Data: largeData}},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CountPromptTokens(large) error = %v", err)
	}
	if small.InputTokens != large.InputTokens {
		t.Fatalf("image bytes affected tiktoken estimate: small=%#v large=%#v", small, large)
	}
}
