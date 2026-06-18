package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	sdkopenai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	sdkresponses "github.com/openai/openai-go/v3/responses"
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
		return llm.Response{}, fmt.Errorf("llm/openai/responses: nil provider")
	}
	if p.apiKey == "" {
		return llm.Response{}, fmt.Errorf("llm/openai/responses: api key is required")
	}
	opts := []option.RequestOption{option.WithAPIKey(p.apiKey)}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkopenai.NewClient(opts...)

	params := sdkresponses.ResponseNewParams{
		Model: shared.ResponsesModel(request.Model),
		Input: sdkresponses.ResponseNewParamsInputUnion{
			OfInputItemList: convertInput(request.Messages),
		},
		Tools: convertTools(request.Tools),
	}
	if len(request.Metadata) > 0 {
		params.Metadata = shared.Metadata(request.Metadata)
	}
	if request.MaxTokens > 0 {
		params.MaxOutputTokens = sdkopenai.Int(request.MaxTokens)
	}
	if request.Temperature != nil {
		params.Temperature = sdkopenai.Float(*request.Temperature)
	}
	if request.ToolChoice != "" {
		params.ToolChoice = sdkresponses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: sdkopenai.Opt(sdkresponses.ToolChoiceOptions(request.ToolChoice)),
		}
	}
	applyReasoning(&params, request.Reasoning)

	response, err := client.Responses.New(ctx, params)
	if err != nil {
		return llm.Response{}, err
	}
	if response == nil {
		return llm.Response{}, fmt.Errorf("llm/openai/responses: empty response")
	}

	text := response.OutputText()
	calls := convertOutputToolCalls(response.Output)
	inputCached := response.Usage.InputTokensDetails.CachedTokens
	return llm.Response{
		ID:        response.ID,
		Model:     string(response.Model),
		Message:   llm.AssistantToolCalls(text, calls),
		Text:      text,
		ToolCalls: calls,
		Usage: llm.Usage{
			InputTokens:         response.Usage.InputTokens,
			InputUncachedTokens: nonNegative(response.Usage.InputTokens - inputCached),
			InputCachedTokens:   inputCached,
			OutputTokens:        response.Usage.OutputTokens,
			ReasoningTokens:     response.Usage.OutputTokensDetails.ReasoningTokens,
			TotalTokens:         response.Usage.TotalTokens,
		},
		StopReason: string(response.Status),
		Raw:        json.RawMessage(response.RawJSON()),
	}, nil
}

func (p *ProviderAdapter) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	if p == nil {
		return llm.Response{}, fmt.Errorf("llm/openai/responses: nil provider")
	}
	if p.apiKey == "" {
		return llm.Response{}, fmt.Errorf("llm/openai/responses: api key is required")
	}
	opts := []option.RequestOption{option.WithAPIKey(p.apiKey)}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkopenai.NewClient(opts...)

	params := sdkresponses.ResponseNewParams{
		Model: shared.ResponsesModel(request.Model),
		Input: sdkresponses.ResponseNewParamsInputUnion{
			OfInputItemList: convertInput(request.Messages),
		},
		Tools: convertTools(request.Tools),
	}
	if len(request.Metadata) > 0 {
		params.Metadata = shared.Metadata(request.Metadata)
	}
	if request.MaxTokens > 0 {
		params.MaxOutputTokens = sdkopenai.Int(request.MaxTokens)
	}
	if request.Temperature != nil {
		params.Temperature = sdkopenai.Float(*request.Temperature)
	}
	if request.ToolChoice != "" {
		params.ToolChoice = sdkresponses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: sdkopenai.Opt(sdkresponses.ToolChoiceOptions(request.ToolChoice)),
		}
	}
	applyReasoning(&params, request.Reasoning)

	stream := client.Responses.NewStreaming(ctx, params)
	defer stream.Close()

	var text strings.Builder
	var final *sdkresponses.Response
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				text.WriteString(event.Delta)
				if err := emitStream(ctx, handler, llm.StreamChunk{Text: event.Delta}); err != nil {
					return llm.Response{}, err
				}
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if event.Delta != "" {
				if err := emitStream(ctx, handler, llm.StreamChunk{Thinking: event.Delta}); err != nil {
					return llm.Response{}, err
				}
			}
		case "response.completed":
			response := event.Response
			final = &response
		case "response.failed", "response.incomplete":
			return llm.Response{}, fmt.Errorf("llm/openai/responses: stream ended with %s", event.Type)
		case "error":
			if event.Message != "" {
				return llm.Response{}, fmt.Errorf("llm/openai/responses: %s", event.Message)
			}
			return llm.Response{}, fmt.Errorf("llm/openai/responses: stream error")
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, err
	}
	if final == nil {
		return llm.Response{}, fmt.Errorf("llm/openai/responses: stream completed without response")
	}

	calls := convertOutputToolCalls(final.Output)
	if text.Len() == 0 {
		text.WriteString(final.OutputText())
	}
	inputCached := final.Usage.InputTokensDetails.CachedTokens
	usage := llm.Usage{
		InputTokens:         final.Usage.InputTokens,
		InputUncachedTokens: nonNegative(final.Usage.InputTokens - inputCached),
		InputCachedTokens:   inputCached,
		OutputTokens:        final.Usage.OutputTokens,
		ReasoningTokens:     final.Usage.OutputTokensDetails.ReasoningTokens,
		TotalTokens:         final.Usage.TotalTokens,
	}
	if usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 {
		if err := emitStream(ctx, handler, llm.StreamChunk{Usage: &usage}); err != nil {
			return llm.Response{}, err
		}
	}
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
		ID:         final.ID,
		Model:      string(final.Model),
		Message:    llm.AssistantToolCalls(text.String(), calls),
		Text:       text.String(),
		ToolCalls:  calls,
		Usage:      usage,
		StopReason: string(final.Status),
		Raw:        json.RawMessage(final.RawJSON()),
	}, nil
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func applyReasoning(params *sdkresponses.ResponseNewParams, config *llm.ReasoningConfig) {
	if params == nil || config == nil {
		return
	}
	var reasoning shared.ReasoningParam
	if effort := openAIReasoningEffort(config); effort != "" {
		reasoning.Effort = shared.ReasoningEffort(effort)
	}
	if config.Summary != "" {
		reasoning.Summary = shared.ReasoningSummary(config.Summary)
	}
	if reasoning.Effort != "" || reasoning.Summary != "" {
		params.Reasoning = reasoning
	}
	if config.IncludeEncryptedContent {
		params.Include = append(params.Include, sdkresponses.ResponseIncludableReasoningEncryptedContent)
	}
}

func openAIReasoningEffort(config *llm.ReasoningConfig) string {
	if config == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(config.Effort))
}

func convertInput(messages []llm.Message) sdkresponses.ResponseInputParam {
	result := make(sdkresponses.ResponseInputParam, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem:
			result = append(result, sdkresponses.ResponseInputItemParamOfMessage(message.Content, sdkresponses.EasyInputMessageRoleSystem))
		case llm.RoleDeveloper:
			result = append(result, sdkresponses.ResponseInputItemParamOfMessage(message.Content, sdkresponses.EasyInputMessageRoleDeveloper))
		case llm.RoleUser:
			result = append(result, sdkresponses.ResponseInputItemParamOfMessage(message.Content, sdkresponses.EasyInputMessageRoleUser))
		case llm.RoleTool:
			result = append(result, sdkresponses.ResponseInputItemParamOfFunctionCallOutput(message.ToolCallID, message.Content))
		case llm.RoleAssistant:
			result = appendAssistantMessage(result, message)
		default:
			result = append(result, sdkresponses.ResponseInputItemParamOfMessage(message.Content, sdkresponses.EasyInputMessageRoleUser))
		}
	}
	return result
}

// Responses API 需要把上一轮工具调用和工具结果都还原成 input item，agent loop 才能继续推理。
func appendAssistantMessage(result sdkresponses.ResponseInputParam, message llm.Message) sdkresponses.ResponseInputParam {
	if message.Content != "" {
		result = append(result, sdkresponses.ResponseInputItemParamOfMessage(message.Content, sdkresponses.EasyInputMessageRoleAssistant))
	}
	for _, call := range message.ToolCalls {
		result = append(result, sdkresponses.ResponseInputItemParamOfFunctionCall(string(call.Arguments), call.ID, call.Name))
	}
	return result
}

func convertTools(tools []llm.ToolSpec) []sdkresponses.ToolUnionParam {
	result := make([]sdkresponses.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		parameters := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		if len(tool.InputSchema) > 0 {
			var decoded map[string]any
			if err := json.Unmarshal(tool.InputSchema, &decoded); err == nil && decoded != nil {
				parameters = decoded
			}
		}
		definition := sdkresponses.FunctionToolParam{
			Name:       tool.Name,
			Parameters: parameters,
			// 这里先不强制 strict，避免轻量 schema 在最小 agent 场景下被过度约束。
			Strict:      sdkopenai.Bool(false),
			Description: sdkopenai.String(tool.Description),
		}
		result = append(result, sdkresponses.ToolUnionParam{OfFunction: &definition})
	}
	return result
}

func convertOutputToolCalls(items []sdkresponses.ResponseOutputItemUnion) []llm.ToolCall {
	calls := make([]llm.ToolCall, 0)
	for _, item := range items {
		if item.Type != "function_call" {
			continue
		}
		call := item.AsFunctionCall()
		id := call.CallID
		if id == "" {
			id = call.ID
		}
		calls = append(calls, llm.ToolCall{
			ID:        id,
			Name:      call.Name,
			Arguments: json.RawMessage(call.Arguments),
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
