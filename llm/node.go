package llm

import (
	"context"
	"errors"

	"github.com/superquail/langquail/graph"
	"github.com/superquail/langquail/trace"
)

type MessageWrite struct {
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

type MessagePolicy[S any] struct {
	Read  func(context.Context, S) ([]Message, error)
	Write func(context.Context, S, MessageWrite) (S, error)
}

type NodeSpec[S any] struct {
	Providers  ProviderSet
	Provider   string
	Model      string
	Messages   MessagePolicy[S]
	Tools      []ToolSpec
	ToolChoice ToolChoice
	MaxTokens  int64
	Stream     bool
	Reasoning  *ReasoningConfig
	Output     func(context.Context, S, Response) (graph.Command[S], error)
	Metadata   map[string]string
}

func Node[S any](id string, spec NodeSpec[S]) graph.NodeSpec[S] {
	metadata := map[string]string{
		"provider": spec.Provider,
		"model":    spec.Model,
	}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	return graph.NodeSpec[S]{
		ID:       id,
		Kind:     graph.NodeKindLLM,
		Metadata: metadata,
		Run: func(ctx context.Context, state S) (graph.Command[S], error) {
			if spec.Messages.Read == nil {
				return graph.Noop[S](), errors.New("llm: message reader is required")
			}
			provider, err := spec.Providers.Get(spec.Provider)
			if err != nil {
				return graph.Noop[S](), err
			}
			messages, err := spec.Messages.Read(ctx, state)
			if err != nil {
				return graph.Noop[S](), err
			}
			if _, err := trace.Emit(ctx, trace.EventMessageRead, map[string]any{"count": len(messages)}); err != nil {
				return graph.Noop[S](), err
			}
			request := Request{
				Provider:   spec.Provider,
				Model:      spec.Model,
				Messages:   append([]Message(nil), messages...),
				Tools:      append([]ToolSpec(nil), spec.Tools...),
				ToolChoice: spec.ToolChoice,
				MaxTokens:  spec.MaxTokens,
				Reasoning:  cloneReasoningConfig(spec.Reasoning),
			}
			if _, err := trace.Emit(ctx, trace.EventPromptRendered, request); err != nil {
				return graph.Noop[S](), err
			}
			if _, err := trace.Emit(ctx, trace.EventLLMStarted, map[string]any{
				"provider": spec.Provider,
				"model":    spec.Model,
			}); err != nil {
				return graph.Noop[S](), err
			}
			var response Response
			if spec.Stream {
				streamProvider, ok := provider.(StreamProvider)
				if !ok {
					return graph.Noop[S](), errors.New("llm: provider does not support streaming")
				}
				response, err = streamProvider.ChatStream(ctx, request, func(chunkCtx context.Context, chunk StreamChunk) error {
					_, emitErr := trace.Emit(chunkCtx, trace.EventLLMDelta, chunk)
					return emitErr
				})
			} else {
				response, err = provider.Chat(ctx, request)
			}
			if err != nil {
				_, _ = trace.Emit(ctx, trace.EventLLMFailed, map[string]any{"error": err.Error()})
				return graph.Noop[S](), err
			}
			if _, err := trace.Emit(ctx, trace.EventLLMCompleted, map[string]any{
				"id":         response.ID,
				"model":      response.Model,
				"usage":      response.Usage,
				"tool_calls": len(response.ToolCalls),
			}); err != nil {
				return graph.Noop[S](), err
			}
			next := state
			if spec.Messages.Write != nil {
				next, err = spec.Messages.Write(ctx, state, MessageWrite{Request: request, Response: response})
				if err != nil {
					return graph.Noop[S](), err
				}
				if _, err := trace.Emit(ctx, trace.EventMessageWritten, map[string]any{
					"role":       response.Message.Role,
					"tool_calls": len(response.ToolCalls),
				}); err != nil {
					return graph.Noop[S](), err
				}
			}
			if spec.Output == nil {
				return graph.Update(next), nil
			}
			command, err := spec.Output(ctx, next, response)
			if err != nil {
				return graph.Noop[S](), err
			}
			if command.Update == nil {
				command.Update = &next
			}
			return command, nil
		},
	}
}

func cloneReasoningConfig(config *ReasoningConfig) *ReasoningConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	if config.Enable != nil {
		enable := *config.Enable
		cloned.Enable = &enable
	}
	return &cloned
}
