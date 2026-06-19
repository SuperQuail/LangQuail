package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
	if e.apiKey == "" {
		return token.Estimate{}, fmt.Errorf("token/anthropic: api key is required")
	}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(e.apiKey)}
	if e.baseURL != "" {
		opts = append(opts, option.WithBaseURL(e.baseURL))
	}
	client := sdkanthropic.NewClient(opts...)
	messages, system, err := convertMessages(request.Messages)
	if err != nil {
		return token.Estimate{}, err
	}
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

func convertMessages(messages []token.Message) ([]sdkanthropic.MessageParam, []sdkanthropic.TextBlockParam, error) {
	result := make([]sdkanthropic.MessageParam, 0, len(messages))
	var system []sdkanthropic.TextBlockParam
	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			blocks, err := convertSystemBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			system = append(system, blocks...)
		case "assistant":
			blocks, err := convertAssistantBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			result = append(result, sdkanthropic.NewAssistantMessage(blocks...))
		case "tool":
			block, err := convertToolResultBlock(message)
			if err != nil {
				return nil, nil, err
			}
			result = append(result, sdkanthropic.NewUserMessage(block))
		default:
			blocks, err := convertContentBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			result = append(result, sdkanthropic.NewUserMessage(blocks...))
		}
	}
	return result, system, nil
}

func convertSystemBlocks(message token.Message) ([]sdkanthropic.TextBlockParam, error) {
	if len(message.Input) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []sdkanthropic.TextBlockParam{{Text: message.Content}}, nil
	}
	blocks := make([]sdkanthropic.TextBlockParam, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case token.InputPartText:
			if part.Text != "" {
				blocks = append(blocks, sdkanthropic.TextBlockParam{Text: part.Text})
			}
		case token.InputPartImage:
			return nil, fmt.Errorf("token/anthropic: image parts cannot be encoded in system or developer messages")
		default:
			return nil, fmt.Errorf("token/anthropic: unsupported input part type %q", part.Type)
		}
	}
	if len(blocks) == 0 && message.Content != "" {
		blocks = append(blocks, sdkanthropic.TextBlockParam{Text: message.Content})
	}
	return blocks, nil
}

func convertContentBlocks(message token.Message) ([]sdkanthropic.ContentBlockParamUnion, error) {
	if len(message.Input) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []sdkanthropic.ContentBlockParamUnion{sdkanthropic.NewTextBlock(message.Content)}, nil
	}
	blocks := make([]sdkanthropic.ContentBlockParamUnion, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case token.InputPartText:
			if part.Text != "" {
				blocks = append(blocks, sdkanthropic.NewTextBlock(part.Text))
			}
		case token.InputPartImage:
			block, err := convertImageBlock(part.Image)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		default:
			return nil, fmt.Errorf("token/anthropic: unsupported input part type %q", part.Type)
		}
	}
	if len(blocks) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []sdkanthropic.ContentBlockParamUnion{sdkanthropic.NewTextBlock(message.Content)}, nil
	}
	return blocks, nil
}

func convertImageBlock(image *token.InputImage) (sdkanthropic.ContentBlockParamUnion, error) {
	if image == nil {
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("token/anthropic: image input is missing image data")
	}
	if image.URL != "" {
		if mimeType, data, ok, err := parseImageDataURL(image.URL); err != nil {
			return sdkanthropic.ContentBlockParamUnion{}, err
		} else if ok {
			return sdkanthropic.NewImageBlock(sdkanthropic.Base64ImageSourceParam{
				Data:      data,
				MediaType: sdkanthropic.Base64ImageSourceMediaType(mimeType),
			}), nil
		}
		return sdkanthropic.NewImageBlock(sdkanthropic.URLImageSourceParam{URL: image.URL}), nil
	}
	if len(image.Data) == 0 {
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("token/anthropic: image input requires url or data")
	}
	if image.MIMEType == "" {
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("token/anthropic: image data requires mime type")
	}
	return sdkanthropic.NewImageBlock(sdkanthropic.Base64ImageSourceParam{
		Data:      base64.StdEncoding.EncodeToString(image.Data),
		MediaType: sdkanthropic.Base64ImageSourceMediaType(image.MIMEType),
	}), nil
}

func parseImageDataURL(value string) (string, string, bool, error) {
	if !strings.HasPrefix(value, "data:") {
		return "", "", false, nil
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return "", "", true, fmt.Errorf("token/anthropic: invalid image data url")
	}
	metadata := value[len("data:"):comma]
	data := value[comma+1:]
	if !strings.Contains(metadata, ";base64") {
		return "", "", true, fmt.Errorf("token/anthropic: image data url must be base64 encoded")
	}
	mediaType := strings.Split(metadata, ";")[0]
	if mediaType == "" {
		return "", "", true, fmt.Errorf("token/anthropic: image data url requires mime type")
	}
	return mediaType, data, true, nil
}

func convertToolResultBlock(message token.Message) (sdkanthropic.ContentBlockParamUnion, error) {
	if len(message.Input) == 0 {
		return sdkanthropic.NewToolResultBlock(message.ToolCallID, message.Content, false), nil
	}
	content := make([]sdkanthropic.ToolResultBlockParamContentUnion, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case token.InputPartText:
			if part.Text != "" {
				content = append(content, sdkanthropic.ToolResultBlockParamContentUnion{
					OfText: &sdkanthropic.TextBlockParam{Text: part.Text},
				})
			}
		case token.InputPartImage:
			block, err := convertImageBlock(part.Image)
			if err != nil {
				return sdkanthropic.ContentBlockParamUnion{}, err
			}
			content = append(content, sdkanthropic.ToolResultBlockParamContentUnion{OfImage: block.OfImage})
		default:
			return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("token/anthropic: unsupported input part type %q", part.Type)
		}
	}
	if len(content) == 0 && message.Content != "" {
		content = append(content, sdkanthropic.ToolResultBlockParamContentUnion{
			OfText: &sdkanthropic.TextBlockParam{Text: message.Content},
		})
	}
	return sdkanthropic.ContentBlockParamUnion{OfToolResult: &sdkanthropic.ToolResultBlockParam{
		ToolUseID: message.ToolCallID,
		Content:   content,
		IsError:   sdkanthropic.Bool(false),
	}}, nil
}

func convertAssistantBlocks(message token.Message) ([]sdkanthropic.ContentBlockParamUnion, error) {
	blocks, err := convertContentBlocks(message)
	if err != nil {
		return nil, err
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
	return blocks, nil
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
