package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	sdkanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/superquail/langquail/token"
)

type Estimator struct {
	apiKey  string
	baseURL string
}

func NewEstimator() *Estimator {
	return &Estimator{}
}

func (e *Estimator) APIKey(value string) *Estimator {
	e.apiKey = value
	return e
}

func (e *Estimator) APIKeyFromEnv(name string) *Estimator {
	e.apiKey = os.Getenv(name)
	return e
}

func (e *Estimator) BaseURL(value string) *Estimator {
	e.baseURL = value
	return e
}

func (e *Estimator) BaseURLFromEnv(name string, fallback string) *Estimator {
	if value := os.Getenv(name); value != "" {
		e.baseURL = value
	} else {
		e.baseURL = fallback
	}
	return e
}

func (e *Estimator) CountPromptTokens(ctx context.Context, request token.EstimateRequest) (token.Estimate, error) {
	if e == nil {
		return token.Estimate{}, fmt.Errorf("token/anthropic: nil estimator")
	}
	if err := token.ValidateRequest(request); err != nil {
		return token.Estimate{}, err
	}
	var opts []option.RequestOption
	if e.apiKey != "" {
		opts = append(opts, option.WithAPIKey(e.apiKey))
	}
	if e.baseURL != "" {
		opts = append(opts, option.WithBaseURL(e.baseURL))
	}
	client := sdkanthropic.NewClient(opts...)
	messages, system := convertMessages(request.Messages)
	count, err := client.Messages.CountTokens(ctx, sdkanthropic.MessageCountTokensParams{
		Model:    sdkanthropic.Model(request.Model),
		Messages: messages,
		System:   systemParam(system),
		Tools:    convertTools(request.Tools),
	})
	if err != nil {
		return token.Estimate{}, err
	}
	return finalize(token.Estimate{
		Provider:        request.Provider,
		Model:           request.Model,
		InputTokens:     count.InputTokens,
		ContextLimit:    request.ContextLimit,
		MaxOutputTokens: request.MaxOutputTokens,
		Source:          token.SourceClaudeAPI,
		Estimated:       true,
	}), nil
}

func systemParam(system []sdkanthropic.TextBlockParam) sdkanthropic.MessageCountTokensParamsSystemUnion {
	if len(system) == 0 {
		return sdkanthropic.MessageCountTokensParamsSystemUnion{}
	}
	return sdkanthropic.MessageCountTokensParamsSystemUnion{OfTextBlockArray: system}
}

func convertMessages(messages []token.Message) ([]sdkanthropic.MessageParam, []sdkanthropic.TextBlockParam) {
	result := make([]sdkanthropic.MessageParam, 0, len(messages))
	var system []sdkanthropic.TextBlockParam
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			system = append(system, sdkanthropic.TextBlockParam{Text: message.Content})
		case "assistant":
			result = append(result, sdkanthropic.NewAssistantMessage(convertAssistantBlocks(message)...))
		case "tool":
			result = append(result, sdkanthropic.NewUserMessage(
				sdkanthropic.NewToolResultBlock(message.ToolCallID, message.Content, false),
			))
		default:
			result = append(result, sdkanthropic.NewUserMessage(sdkanthropic.NewTextBlock(message.Content)))
		}
	}
	return result, system
}

func convertAssistantBlocks(message token.Message) []sdkanthropic.ContentBlockParamUnion {
	blocks := make([]sdkanthropic.ContentBlockParamUnion, 0, len(message.ToolCalls)+1)
	if message.Content != "" {
		blocks = append(blocks, sdkanthropic.NewTextBlock(message.Content))
	}
	for _, call := range message.ToolCalls {
		var input any
		if len(call.Arguments) > 0 {
			if err := json.Unmarshal(call.Arguments, &input); err != nil {
				input = map[string]any{"raw": string(call.Arguments)}
			}
		}
		blocks = append(blocks, sdkanthropic.NewToolUseBlock(call.ID, input, call.Name))
	}
	return blocks
}

func convertTools(tools []token.ToolSpec) []sdkanthropic.MessageCountTokensToolUnionParam {
	result := make([]sdkanthropic.MessageCountTokensToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		param := sdkanthropic.MessageCountTokensToolParamOfTool(toAnthropicSchema(tool.InputSchema), tool.Name)
		if param.OfTool != nil && tool.Description != "" {
			param.OfTool.Description = sdkanthropic.String(tool.Description)
		}
		result = append(result, param)
	}
	return result
}

func toAnthropicSchema(raw json.RawMessage) sdkanthropic.ToolInputSchemaParam {
	var schema map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &schema)
	}
	if len(schema) == 0 {
		schema = map[string]any{"type": "object"}
	}
	return sdkanthropic.ToolInputSchemaParam{
		Properties:  schema["properties"],
		Required:    stringSlice(schema["required"]),
		ExtraFields: schema,
	}
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func finalize(estimate token.Estimate) token.Estimate {
	if estimate.ContextLimit > 0 {
		estimate.RemainingTokens = estimate.ContextLimit - estimate.InputTokens - estimate.MaxOutputTokens
	}
	return estimate
}
