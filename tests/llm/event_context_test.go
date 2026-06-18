package llm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/prompt"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

func TestLLMNodePromptRenderedEventContextIncludesRequest(t *testing.T) {
	provider := &directProvider{name: "fake"}
	g := graph.NewStateGraph[directNodeState]("llm.context.rendered")
	g.Node(llm.Node("answer", llm.NodeSpec[directNodeState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Messages: llm.MessagePolicy[directNodeState]{
			Read: func(ctx context.Context, state directNodeState) ([]llm.Message, error) {
				return state.Messages, nil
			},
		},
	}))
	g.Start("answer")
	g.Finish("answer")

	var rendered trace.Event
	runner, err := lqruntime.NewRunner(
		g,
		lqruntime.WithEventContext[directNodeState](lqruntime.EventContextOptions{Enabled: true}),
		lqruntime.WithEventHandler[directNodeState](func(ctx context.Context, event trace.Event) error {
			if event.Type == trace.EventPromptRendered {
				rendered = event
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), directNodeState{Messages: []llm.Message{llm.User("hello")}})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if rendered.Context == nil || len(rendered.Context.Current.State) == 0 || len(rendered.Context.Current.LLMRequest) == 0 {
		t.Fatalf("prompt.rendered context = %#v", rendered.Context)
	}
	var request llm.Request
	if err := json.Unmarshal(rendered.Context.Current.LLMRequest, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Model != "test-model" || len(request.Messages) != 1 || request.Messages[0].Content != "hello" {
		t.Fatalf("request = %#v", request)
	}
	for _, event := range result.Events {
		if event.Type == trace.EventPromptRendered && event.Context != nil {
			t.Fatalf("recorded prompt.rendered context = %#v, want nil", event.Context)
		}
	}
}

func TestCompactMessagesEventContextIncludesBeforeAfterMessages(t *testing.T) {
	g := graph.NewStateGraph[directNodeState]("llm.context.adjusted")
	g.Step("compact", func(ctx context.Context, state directNodeState) (graph.Command[directNodeState], error) {
		compacted, _, err := llm.CompactMessages(ctx, state.Messages, prompt.CompactPlan{Ops: []prompt.CompactOp{
			prompt.DropSegment(llm.MessageSegmentID(0)),
			prompt.AddSegment(prompt.Segment{
				ID:      "summary",
				Role:    string(llm.RoleUser),
				Content: "summary",
			}, prompt.PositionStart()),
		}})
		if err != nil {
			return graph.Noop[directNodeState](), err
		}
		state.Messages = compacted
		return graph.Update(state), nil
	})
	g.Start("compact")
	g.Finish("compact")

	var adjusted trace.Event
	runner, err := lqruntime.NewRunner(
		g,
		lqruntime.WithEventContext[directNodeState](lqruntime.EventContextOptions{Enabled: true}),
		lqruntime.WithEventHandler[directNodeState](func(ctx context.Context, event trace.Event) error {
			if event.Type == trace.EventPromptAdjusted {
				adjusted = event
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	if _, err := runner.Invoke(context.Background(), directNodeState{Messages: []llm.Message{llm.User("drop"), llm.User("keep")}}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if adjusted.Context == nil || adjusted.Context.Change == nil {
		t.Fatalf("prompt.adjusted context = %#v", adjusted.Context)
	}
	if len(adjusted.Context.Current.Messages) == 0 || len(adjusted.Context.Current.Prompt) == 0 || len(adjusted.Context.Change.Before) == 0 || len(adjusted.Context.Change.After) == 0 || len(adjusted.Context.Change.Ops) == 0 {
		t.Fatalf("prompt.adjusted context = %#v", adjusted.Context)
	}
}
