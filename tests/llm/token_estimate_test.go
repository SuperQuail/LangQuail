package llm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/token"
	"github.com/superquail/langquail/trace"
)

type estimateState struct {
	Messages []llm.Message
}

func TestLLMNodeEmitsPromptEstimateFromContextEstimator(t *testing.T) {
	provider := &estimateProvider{name: "fake"}
	g := graph.NewStateGraph[estimateState]("llm.estimate")
	g.Node(llm.Node("call", llm.NodeSpec[estimateState]{
		Providers:    llm.Providers(provider),
		Provider:     "fake",
		Model:        "fake-model",
		ContextLimit: 100,
		Messages: llm.MessagePolicy[estimateState]{
			Read: func(context.Context, estimateState) ([]llm.Message, error) {
				return []llm.Message{llm.User("estimate this prompt")}, nil
			},
		},
	}))
	g.Start("call")
	g.Finish("call")

	var estimate token.Estimate
	runner, err := lqruntime.NewRunner(g, lqruntime.WithEventHandler[estimateState](func(ctx context.Context, event trace.Event) error {
		if event.Type != trace.EventPromptEstimated {
			return nil
		}
		return json.Unmarshal(event.Payload, &estimate)
	}))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	ctx := token.WithEstimator(context.Background(), token.NewTiktokenEstimator())
	result, err := runner.Invoke(ctx, estimateState{})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Run.Status != lqruntime.StatusCompleted {
		t.Fatalf("status = %s", result.Run.Status)
	}
	if provider.requests != 1 {
		t.Fatalf("provider requests = %d", provider.requests)
	}
	if estimate.Source != token.SourceTiktoken || estimate.InputTokens <= 0 || estimate.ContextLimit != 100 {
		t.Fatalf("estimate = %#v", estimate)
	}
}

type estimateProvider struct {
	name     string
	requests int
}

func (p *estimateProvider) Name() string {
	return p.name
}

func (p *estimateProvider) Chat(context.Context, llm.Request) (llm.Response, error) {
	p.requests++
	return llm.Response{
		Model:   "fake-model",
		Message: llm.Assistant("ok"),
		Text:    "ok",
	}, nil
}
