package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	sdkopenai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/superquail/langquail/llm"
)

type ProviderAdapter struct {
	name    string
	apiKey  string
	baseURL string
}

func Provider(name string) *ProviderAdapter {
	return &ProviderAdapter{name: name}
}

func (p *ProviderAdapter) Name() string {
	if p == nil {
		return ""
	}
	return p.name
}

func (p *ProviderAdapter) APIKey(value string) *ProviderAdapter {
	p.apiKey = value
	return p
}

func (p *ProviderAdapter) APIKeyFromEnv(name string) *ProviderAdapter {
	p.apiKey = os.Getenv(name)
	return p
}

func (p *ProviderAdapter) BaseURL(value string) *ProviderAdapter {
	p.baseURL = value
	return p
}

func (p *ProviderAdapter) BaseURLFromEnv(name string, fallback string) *ProviderAdapter {
	if value := os.Getenv(name); value != "" {
		p.baseURL = value
	} else {
		p.baseURL = fallback
	}
	return p
}

func (p *ProviderAdapter) Chat(ctx context.Context, request llm.Request) (llm.Response, error) {
	if p == nil {
		return llm.Response{}, fmt.Errorf("llm/openai: nil provider")
	}
	var opts []option.RequestOption
	if p.apiKey != "" {
		opts = append(opts, option.WithAPIKey(p.apiKey))
	}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkopenai.NewClient(opts...)

	params := sdkopenai.ChatCompletionNewParams{
		Model:    sdkopenai.ChatModel(request.Model),
		Messages: convertMessages(request.Messages),
		Tools:    convertTools(request.Tools),
	}
	if request.MaxTokens > 0 {
		params.MaxCompletionTokens = sdkopenai.Int(request.MaxTokens)
	}
	if request.Temperature != nil {
		params.Temperature = sdkopenai.Float(*request.Temperature)
	}
	if request.ToolChoice != "" {
		params.ToolChoice = sdkopenai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: sdkopenai.String(string(request.ToolChoice)),
		}
	}
	if effort := openAIReasoningEffort(request.Reasoning); effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(effort)
	}

	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return llm.Response{}, err
	}
	if completion == nil || len(completion.Choices) == 0 {
		return llm.Response{}, fmt.Errorf("llm/openai: empty chat completion")
	}

	message := completion.Choices[0].Message
	calls := make([]llm.ToolCall, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			continue
		}
		calls = append(calls, llm.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	responseMessage := llm.AssistantToolCalls(message.Content, calls)
	inputCached := completion.Usage.PromptTokensDetails.CachedTokens
	return llm.Response{
		ID:        completion.ID,
		Model:     completion.Model,
		Message:   responseMessage,
		Text:      message.Content,
		ToolCalls: calls,
		Usage: llm.Usage{
			InputTokens:         completion.Usage.PromptTokens,
			InputUncachedTokens: nonNegative(completion.Usage.PromptTokens - inputCached),
			InputCachedTokens:   inputCached,
			OutputTokens:        completion.Usage.CompletionTokens,
			ReasoningTokens:     completion.Usage.CompletionTokensDetails.ReasoningTokens,
			TotalTokens:         completion.Usage.TotalTokens,
		},
		StopReason: string(completion.Choices[0].FinishReason),
		Raw:        json.RawMessage(completion.RawJSON()),
	}, nil
}

func (p *ProviderAdapter) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	if p == nil {
		return llm.Response{}, fmt.Errorf("llm/openai: nil provider")
	}
	var opts []option.RequestOption
	if p.apiKey != "" {
		opts = append(opts, option.WithAPIKey(p.apiKey))
	}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkopenai.NewClient(opts...)

	params := sdkopenai.ChatCompletionNewParams{
		Model:    sdkopenai.ChatModel(request.Model),
		Messages: convertMessages(request.Messages),
		Tools:    convertTools(request.Tools),
		StreamOptions: sdkopenai.ChatCompletionStreamOptionsParam{
			IncludeUsage: sdkopenai.Bool(true),
		},
	}
	if request.MaxTokens > 0 {
		params.MaxCompletionTokens = sdkopenai.Int(request.MaxTokens)
	}
	if request.Temperature != nil {
		params.Temperature = sdkopenai.Float(*request.Temperature)
	}
	if request.ToolChoice != "" {
		params.ToolChoice = sdkopenai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: sdkopenai.String(string(request.ToolChoice)),
		}
	}
	if effort := openAIReasoningEffort(request.Reasoning); effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(effort)
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var text strings.Builder
	var responseID string
	var model string
	var stopReason string
	var usage llm.Usage
	partials := make(map[int64]*streamToolCall)
	var order []int64

	for stream.Next() {
		chunk := stream.Current()
		if chunk.ID != "" {
			responseID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			inputCached := chunk.Usage.PromptTokensDetails.CachedTokens
			usage = llm.Usage{
				InputTokens:         chunk.Usage.PromptTokens,
				InputUncachedTokens: nonNegative(chunk.Usage.PromptTokens - inputCached),
				InputCachedTokens:   inputCached,
				OutputTokens:        chunk.Usage.CompletionTokens,
				ReasoningTokens:     chunk.Usage.CompletionTokensDetails.ReasoningTokens,
				TotalTokens:         chunk.Usage.TotalTokens,
			}
			if err := emitStream(ctx, handler, llm.StreamChunk{Usage: &usage}); err != nil {
				return llm.Response{}, err
			}
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				stopReason = string(choice.FinishReason)
			}
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if err := emitStream(ctx, handler, llm.StreamChunk{Text: choice.Delta.Content}); err != nil {
					return llm.Response{}, err
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				partial, exists := partials[call.Index]
				if !exists {
					partial = &streamToolCall{}
					partials[call.Index] = partial
					order = append(order, call.Index)
				}
				if call.ID != "" {
					partial.id = call.ID
				}
				if call.Function.Name != "" {
					partial.name = call.Function.Name
				}
				if call.Function.Arguments != "" {
					partial.arguments.WriteString(call.Function.Arguments)
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, err
	}

	calls := completeStreamToolCalls(order, partials)
	for _, call := range calls {
		current := call
		if err := emitStream(ctx, handler, llm.StreamChunk{ToolCall: &current}); err != nil {
			return llm.Response{}, err
		}
	}
	if err := emitStream(ctx, handler, llm.StreamChunk{Done: true}); err != nil {
		return llm.Response{}, err
	}
	return llm.Response{
		ID:         responseID,
		Model:      model,
		Message:    llm.AssistantToolCalls(text.String(), calls),
		Text:       text.String(),
		ToolCalls:  calls,
		Usage:      usage,
		StopReason: stopReason,
	}, nil
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func openAIReasoningEffort(config *llm.ReasoningConfig) string {
	if config == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(config.Effort))
}

type streamToolCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func completeStreamToolCalls(order []int64, partials map[int64]*streamToolCall) []llm.ToolCall {
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	calls := make([]llm.ToolCall, 0, len(order))
	for _, index := range order {
		partial := partials[index]
		if partial == nil || partial.name == "" {
			continue
		}
		calls = append(calls, llm.ToolCall{
			ID:        partial.id,
			Name:      partial.name,
			Arguments: json.RawMessage(partial.arguments.String()),
		})
	}
	return calls
}

func emitStream(ctx context.Context, handler llm.StreamHandler, chunk llm.StreamChunk) error {
	if handler == nil {
		return nil
	}
	return handler(ctx, chunk)
}

func convertMessages(messages []llm.Message) []sdkopenai.ChatCompletionMessageParamUnion {
	result := make([]sdkopenai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem:
			result = append(result, sdkopenai.SystemMessage(message.Content))
		case llm.RoleDeveloper:
			result = append(result, sdkopenai.DeveloperMessage(message.Content))
		case llm.RoleUser:
			result = append(result, sdkopenai.UserMessage(message.Content))
		case llm.RoleTool:
			result = append(result, sdkopenai.ToolMessage(message.Content, message.ToolCallID))
		case llm.RoleAssistant:
			result = append(result, convertAssistantMessage(message))
		default:
			result = append(result, sdkopenai.UserMessage(message.Content))
		}
	}
	return result
}

func convertAssistantMessage(message llm.Message) sdkopenai.ChatCompletionMessageParamUnion {
	if len(message.ToolCalls) == 0 {
		return sdkopenai.AssistantMessage(message.Content)
	}
	assistant := sdkopenai.ChatCompletionAssistantMessageParam{
		ToolCalls: convertToolCallParams(message.ToolCalls),
	}
	if message.Content != "" {
		assistant.Content = sdkopenai.ChatCompletionAssistantMessageParamContentUnion{
			OfString: sdkopenai.String(message.Content),
		}
	}
	return sdkopenai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
}

func convertToolCallParams(calls []llm.ToolCall) []sdkopenai.ChatCompletionMessageToolCallUnionParam {
	result := make([]sdkopenai.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	for _, call := range calls {
		result = append(result, sdkopenai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &sdkopenai.ChatCompletionMessageFunctionToolCallParam{
				ID: call.ID,
				Function: sdkopenai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      call.Name,
					Arguments: string(call.Arguments),
				},
			},
		})
	}
	return result
}

func convertTools(tools []llm.ToolSpec) []sdkopenai.ChatCompletionToolUnionParam {
	result := make([]sdkopenai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		parameters := shared.FunctionParameters{}
		if len(tool.InputSchema) > 0 {
			_ = json.Unmarshal(tool.InputSchema, &parameters)
		}
		definition := shared.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: sdkopenai.String(tool.Description),
			Parameters:  parameters,
		}
		result = append(result, sdkopenai.ChatCompletionFunctionTool(definition))
	}
	return result
}
