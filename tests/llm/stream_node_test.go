package llm_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/llm"
	lqruntime "github.com/superquail/langquail/runtime"
	"github.com/superquail/langquail/trace"
)

type streamState struct {
	Messages []llm.Message
	Answer   string
}

func TestLLMNodeStreamingEmitsDeltaEvents(t *testing.T) {
	provider := fakeStreamProvider{name: "fake"}
	g := graph.NewStateGraph[streamState]("llm.stream")
	g.Node(llm.Node("answer", llm.NodeSpec[streamState]{
		Providers: llm.Providers(provider),
		Provider:  "fake",
		Model:     "test-model",
		Stream:    true,
		Messages: llm.MessagePolicy[streamState]{
			Read: func(ctx context.Context, state streamState) ([]llm.Message, error) {
				return state.Messages, nil
			},
			Write: func(ctx context.Context, state streamState, write llm.MessageWrite) (streamState, error) {
				state.Messages = append(state.Messages, write.Response.Message)
				return state, nil
			},
		},
		Output: func(ctx context.Context, state streamState, response llm.Response) (graph.Command[streamState], error) {
			state.Answer = response.Text
			return graph.Command[streamState]{Update: &state, End: true}, nil
		},
	}))
	g.Start("answer")

	var deltas []string
	runner, err := lqruntime.NewRunner(g, lqruntime.WithEventHandler[streamState](func(ctx context.Context, event trace.Event) error {
		if event.Type != trace.EventLLMDelta {
			return nil
		}
		var chunk llm.StreamChunk
		if err := json.Unmarshal(event.Payload, &chunk); err != nil {
			return err
		}
		if chunk.Text != "" {
			deltas = append(deltas, chunk.Text)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	result, err := runner.Invoke(context.Background(), streamState{
		Messages: []llm.Message{llm.User("hello")},
	})
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.State.Answer != "Hello stream." {
		t.Fatalf("Answer = %q", result.State.Answer)
	}
	if got := deltas; len(got) != 2 || got[0] != "Hello " || got[1] != "stream." {
		t.Fatalf("deltas = %#v", got)
	}
}

type fakeStreamProvider struct {
	name string
}

func (p fakeStreamProvider) Name() string {
	return p.name
}

func (p fakeStreamProvider) Chat(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (p fakeStreamProvider) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	if err := handler(ctx, llm.StreamChunk{Text: "Hello "}); err != nil {
		return llm.Response{}, err
	}
	if err := handler(ctx, llm.StreamChunk{Text: "stream."}); err != nil {
		return llm.Response{}, err
	}
	if err := handler(ctx, llm.StreamChunk{Done: true}); err != nil {
		return llm.Response{}, err
	}
	text := "Hello stream."
	return llm.Response{
		ID:      "resp_fake",
		Model:   request.Model,
		Message: llm.Assistant(text),
		Text:    text,
	}, nil
}
