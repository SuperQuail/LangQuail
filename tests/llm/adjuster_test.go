package llm_test

import (
	"context"
	"testing"

	"github.com/superquail/langquail/llm"
)

func TestLLMNodeRunsContextAdjusterBeforeProvider(t *testing.T) {
	provider := &directProvider{name: "fake"}
	adjuster := &beforeLLMAdjuster{}
	node := llm.Node("answer", llm.NodeSpec[directNodeState]{
		Providers:    llm.Providers(provider),
		Provider:     "fake",
		Model:        "test-model",
		ContextLimit: 100,
		MaxTokens:    10,
		Messages: llm.MessagePolicy[directNodeState]{
			Read: func(ctx context.Context, state directNodeState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	})

	ctx := llm.WithAdjuster(context.Background(), adjuster)
	if _, err := node.Run(ctx, directNodeState{Messages: []llm.Message{llm.User("original")}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(adjuster.requests) != 1 {
		t.Fatalf("requests = %#v", adjuster.requests)
	}
	request := adjuster.requests[0]
	if request.NodeID != "answer" || request.Model != "test-model" || request.Budget.ContextLimit != 100 || request.Budget.MaxOutputTokens != 10 {
		t.Fatalf("adjuster request = %#v", request)
	}
	if len(provider.requests) != 1 || provider.requests[0].Messages[0].Content != "adjusted" {
		t.Fatalf("provider requests = %#v", provider.requests)
	}
}

type beforeLLMAdjuster struct {
	requests []llm.BeforeLLMRequest
}

func (a *beforeLLMAdjuster) BeforeLLM(ctx context.Context, request llm.BeforeLLMRequest) (llm.BeforeLLMResult, error) {
	a.requests = append(a.requests, request)
	return llm.BeforeLLMResult{
		Messages: []llm.Message{llm.User("adjusted")},
	}, nil
}
