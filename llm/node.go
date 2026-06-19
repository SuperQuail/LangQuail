package llm

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/superquail/langquail/graph"
	lqprompt "github.com/superquail/langquail/prompt"
	lqtoken "github.com/superquail/langquail/token"
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
	Providers      ProviderSet
	Provider       string
	Model          string
	PromptID       string
	Data           func(context.Context, S) (map[string]any, error)
	Messages       MessagePolicy[S]
	Tools          []ToolSpec
	ToolIDs        []string
	ToolChoice     ToolChoice
	MaxTokens      int64
	ContextLimit   int64
	TokenEstimator lqtoken.Estimator
	Stream         bool
	Reasoning      *ReasoningConfig
	Output         func(context.Context, S, Response) (graph.Command[S], error)
	Metadata       map[string]string
}

func Node[S any](id string, spec NodeSpec[S]) graph.NodeSpec[S] {
	metadata := map[string]string{
		"provider": spec.Provider,
		"model":    spec.Model,
	}
	if spec.PromptID != "" {
		metadata["prompt_id"] = spec.PromptID
	}
	if len(spec.ToolIDs) > 0 {
		metadata["tool_ids"] = strings.Join(spec.ToolIDs, ",")
	}
	for key, value := range spec.Metadata {
		metadata[key] = value
	}
	return graph.NodeSpec[S]{
		ID:       id,
		Kind:     graph.NodeKindLLM,
		Metadata: metadata,
		Run: func(ctx context.Context, state S) (graph.Command[S], error) {
			providers := spec.Providers
			if len(providers.providers) == 0 {
				if contextual, ok := ProvidersFromContext(ctx); ok {
					providers = contextual
				}
			}
			provider, err := providers.Get(spec.Provider)
			if err != nil {
				return graph.Noop[S](), err
			}
			messages, err := resolveMessages(ctx, state, spec)
			if err != nil {
				return graph.Noop[S](), err
			}
			tools, err := resolveToolSpecs(ctx, spec)
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
				Tools:      tools,
				ToolChoice: spec.ToolChoice,
				MaxTokens:  spec.MaxTokens,
				Reasoning:  cloneReasoningConfig(spec.Reasoning),
			}
			request, err = applyBeforeLLMAdjuster(ctx, id, spec, request)
			if err != nil {
				return graph.Noop[S](), err
			}
			if _, err := trace.Emit(ctx, trace.EventPromptRendered, request); err != nil {
				return graph.Noop[S](), err
			}
			if err := estimatePromptTokens(ctx, spec, request); err != nil {
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

func applyBeforeLLMAdjuster[S any](ctx context.Context, nodeID string, spec NodeSpec[S], request Request) (Request, error) {
	adjuster, ok := AdjusterFromContext(ctx)
	if !ok {
		return request, nil
	}
	result, err := adjuster.BeforeLLM(ctx, BeforeLLMRequest{
		NodeID:   nodeID,
		Provider: request.Provider,
		Model:    request.Model,
		PromptID: spec.PromptID,
		Messages: cloneMessages(request.Messages),
		Tools:    cloneToolSpecs(request.Tools),
		Budget: lqtoken.Budget{
			ContextLimit:    spec.ContextLimit,
			MaxOutputTokens: spec.MaxTokens,
		},
		Metadata: cloneStringMap(spec.Metadata),
	})
	if err != nil {
		return Request{}, err
	}
	if result.Messages != nil {
		request.Messages = cloneMessages(result.Messages)
	}
	return request, nil
}

func resolveMessages[S any](ctx context.Context, state S, spec NodeSpec[S]) ([]Message, error) {
	if spec.PromptID != "" && spec.Messages.Read != nil {
		return nil, errors.New("llm: PromptID and Messages.Read cannot both be set")
	}
	if spec.PromptID == "" {
		if spec.Messages.Read == nil {
			return nil, errors.New("llm: message reader is required")
		}
		return spec.Messages.Read(ctx, state)
	}
	registry, ok := lqprompt.RegistryFromContext(ctx)
	if !ok {
		return nil, errors.New("llm: prompt registry is required for PromptID")
	}
	data := map[string]any{}
	if spec.Data != nil {
		renderData, err := spec.Data(ctx, state)
		if err != nil {
			return nil, err
		}
		if renderData != nil {
			data = renderData
		}
	}
	rendered, err := registry.Render(ctx, spec.PromptID, data)
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(rendered.Segments))
	for _, segment := range rendered.Segments {
		messages = append(messages, Message{
			Role:    Role(segment.Role),
			Content: segment.Content,
		})
	}
	return messages, nil
}

func resolveToolSpecs[S any](ctx context.Context, spec NodeSpec[S]) ([]ToolSpec, error) {
	if len(spec.Tools) > 0 && len(spec.ToolIDs) > 0 {
		return nil, errors.New("llm: Tools and ToolIDs cannot both be set")
	}
	if len(spec.ToolIDs) == 0 {
		return append([]ToolSpec(nil), spec.Tools...), nil
	}
	resolver, ok := ToolSpecResolverFromContext(ctx)
	if !ok {
		return nil, errors.New("llm: tool resolver is required for ToolIDs")
	}
	tools, err := resolver.LLMSpecs(spec.ToolIDs...)
	if err != nil {
		return nil, err
	}
	return append([]ToolSpec(nil), tools...), nil
}

func estimatePromptTokens[S any](ctx context.Context, spec NodeSpec[S], request Request) error {
	estimator := spec.TokenEstimator
	if estimator == nil {
		if contextual, ok := lqtoken.EstimatorFromContext(ctx); ok {
			estimator = contextual
		}
	}
	if estimator == nil {
		return nil
	}
	estimate, err := estimator.CountPromptTokens(ctx, lqtoken.EstimateRequest{
		Provider:        request.Provider,
		Model:           request.Model,
		Messages:        toTokenMessages(request.Messages),
		Tools:           toTokenTools(request.Tools),
		MaxOutputTokens: request.MaxTokens,
		ContextLimit:    spec.ContextLimit,
		Metadata:        request.Metadata,
	})
	if err != nil {
		return err
	}
	_, err = trace.Emit(ctx, trace.EventPromptEstimated, estimate)
	return err
}

func toTokenMessages(messages []Message) []lqtoken.Message {
	result := make([]lqtoken.Message, 0, len(messages))
	for _, message := range messages {
		result = append(result, lqtoken.Message{
			Role:       string(message.Role),
			Content:    message.Content,
			Input:      toTokenInputParts(message.Input),
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
			ToolCalls:  toTokenToolCalls(message.ToolCalls),
		})
	}
	return result
}

func toTokenInputParts(parts []InputPart) []lqtoken.InputPart {
	if len(parts) == 0 {
		return nil
	}
	result := make([]lqtoken.InputPart, 0, len(parts))
	for _, part := range parts {
		converted := lqtoken.InputPart{
			Type: lqtoken.InputPartType(part.Type),
			Text: part.Text,
		}
		if part.Image != nil {
			converted.Image = &lqtoken.InputImage{
				URL:      part.Image.URL,
				Data:     bytes.Clone(part.Image.Data),
				MIMEType: part.Image.MIMEType,
			}
		}
		result = append(result, converted)
	}
	return result
}

func toTokenToolCalls(calls []ToolCall) []lqtoken.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	result := make([]lqtoken.ToolCall, 0, len(calls))
	for _, call := range calls {
		result = append(result, lqtoken.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		})
	}
	return result
}

func toTokenTools(tools []ToolSpec) []lqtoken.ToolSpec {
	if len(tools) == 0 {
		return nil
	}
	result := make([]lqtoken.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		result = append(result, lqtoken.ToolSpec{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	return result
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
