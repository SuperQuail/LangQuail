package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	sdkanthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

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
		return llm.Response{}, fmt.Errorf("llm/anthropic: nil provider")
	}
	if p.apiKey == "" {
		return llm.Response{}, fmt.Errorf("llm/anthropic: api key is required")
	}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(p.apiKey)}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkanthropic.NewClient(opts...)

	messages, system, err := convertMessages(request.Messages)
	if err != nil {
		return llm.Response{}, err
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	params := sdkanthropic.MessageNewParams{
		Model:        sdkanthropic.Model(request.Model),
		MaxTokens:    maxTokens,
		Messages:     messages,
		System:       system,
		Tools:        convertTools(request.Tools),
		Thinking:     thinkingParam(request.Reasoning),
		OutputConfig: outputConfigParam(request.Reasoning),
	}
	if request.Temperature != nil {
		params.Temperature = sdkanthropic.Float(*request.Temperature)
	}

	message, err := client.Messages.New(ctx, params)
	if err != nil {
		return llm.Response{}, err
	}
	if message == nil {
		return llm.Response{}, fmt.Errorf("llm/anthropic: empty message")
	}
	text, calls := convertResponseContent(message.Content)
	responseMessage := llm.AssistantToolCalls(text, calls)
	inputUncached := message.Usage.InputTokens
	inputCacheCreation := message.Usage.CacheCreationInputTokens
	inputCached := message.Usage.CacheReadInputTokens
	totalInput := inputUncached + inputCacheCreation + inputCached
	return llm.Response{
		ID:        message.ID,
		Model:     string(message.Model),
		Message:   responseMessage,
		Text:      text,
		ToolCalls: calls,
		Usage: llm.Usage{
			InputTokens:              totalInput,
			InputUncachedTokens:      inputUncached,
			InputCachedTokens:        inputCached,
			InputCacheCreationTokens: inputCacheCreation,
			OutputTokens:             message.Usage.OutputTokens,
			TotalTokens:              totalInput + message.Usage.OutputTokens,
		},
		StopReason: string(message.StopReason),
		Raw:        json.RawMessage(message.RawJSON()),
	}, nil
}

func (p *ProviderAdapter) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	if p == nil {
		return llm.Response{}, fmt.Errorf("llm/anthropic: nil provider")
	}
	if p.apiKey == "" {
		return llm.Response{}, fmt.Errorf("llm/anthropic: api key is required")
	}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(p.apiKey)}
	if p.baseURL != "" {
		opts = append(opts, option.WithBaseURL(p.baseURL))
	}
	client := sdkanthropic.NewClient(opts...)

	messages, system, err := convertMessages(request.Messages)
	if err != nil {
		return llm.Response{}, err
	}
	maxTokens := request.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	params := sdkanthropic.MessageNewParams{
		Model:        sdkanthropic.Model(request.Model),
		MaxTokens:    maxTokens,
		Messages:     messages,
		System:       system,
		Tools:        convertTools(request.Tools),
		Thinking:     thinkingParam(request.Reasoning),
		OutputConfig: outputConfigParam(request.Reasoning),
	}
	if request.Temperature != nil {
		params.Temperature = sdkanthropic.Float(*request.Temperature)
	}

	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var message sdkanthropic.Message
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return llm.Response{}, err
		}
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			if err := emitStream(ctx, handler, llm.StreamChunk{Text: event.Delta.Text}); err != nil {
				return llm.Response{}, err
			}
		}
		if event.Type == "content_block_delta" && event.Delta.Thinking != "" {
			if err := emitStream(ctx, handler, llm.StreamChunk{Thinking: event.Delta.Thinking}); err != nil {
				return llm.Response{}, err
			}
		}
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, err
	}

	text, calls := convertResponseContent(message.Content)
	inputUncached := message.Usage.InputTokens
	inputCacheCreation := message.Usage.CacheCreationInputTokens
	inputCached := message.Usage.CacheReadInputTokens
	totalInput := inputUncached + inputCacheCreation + inputCached
	usage := llm.Usage{
		InputTokens:              totalInput,
		InputUncachedTokens:      inputUncached,
		InputCachedTokens:        inputCached,
		InputCacheCreationTokens: inputCacheCreation,
		OutputTokens:             message.Usage.OutputTokens,
		TotalTokens:              totalInput + message.Usage.OutputTokens,
	}
	if usage.TotalTokens > 0 || usage.OutputTokens > 0 || usage.InputTokens > 0 {
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
		ID:         message.ID,
		Model:      string(message.Model),
		Message:    llm.AssistantToolCalls(text, calls),
		Text:       text,
		ToolCalls:  calls,
		Usage:      usage,
		StopReason: string(message.StopReason),
		Raw:        json.RawMessage(message.RawJSON()),
	}, nil
}

func convertMessages(messages []llm.Message) ([]sdkanthropic.MessageParam, []sdkanthropic.TextBlockParam, error) {
	result := make([]sdkanthropic.MessageParam, 0, len(messages))
	var system []sdkanthropic.TextBlockParam
	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			blocks, err := convertSystemBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			system = append(system, blocks...)
		case llm.RoleAssistant:
			blocks, err := convertAssistantBlocks(message)
			if err != nil {
				return nil, nil, err
			}
			result = append(result, sdkanthropic.NewAssistantMessage(blocks...))
		case llm.RoleTool:
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

func convertSystemBlocks(message llm.Message) ([]sdkanthropic.TextBlockParam, error) {
	if len(message.Input) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []sdkanthropic.TextBlockParam{{Text: message.Content}}, nil
	}
	blocks := make([]sdkanthropic.TextBlockParam, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case llm.InputPartText:
			if part.Text != "" {
				blocks = append(blocks, sdkanthropic.TextBlockParam{Text: part.Text})
			}
		case llm.InputPartImage:
			return nil, fmt.Errorf("llm/anthropic: image parts cannot be encoded in system or developer messages")
		default:
			return nil, fmt.Errorf("llm/anthropic: unsupported input part type %q", part.Type)
		}
	}
	if len(blocks) == 0 && message.Content != "" {
		blocks = append(blocks, sdkanthropic.TextBlockParam{Text: message.Content})
	}
	return blocks, nil
}

func convertContentBlocks(message llm.Message) ([]sdkanthropic.ContentBlockParamUnion, error) {
	if len(message.Input) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []sdkanthropic.ContentBlockParamUnion{sdkanthropic.NewTextBlock(message.Content)}, nil
	}
	blocks := make([]sdkanthropic.ContentBlockParamUnion, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case llm.InputPartText:
			if part.Text != "" {
				blocks = append(blocks, sdkanthropic.NewTextBlock(part.Text))
			}
		case llm.InputPartImage:
			block, err := convertImageBlock(part.Image)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		default:
			return nil, fmt.Errorf("llm/anthropic: unsupported input part type %q", part.Type)
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

func convertImageBlock(image *llm.InputImage) (sdkanthropic.ContentBlockParamUnion, error) {
	if image == nil {
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("llm/anthropic: image input is missing image data")
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
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("llm/anthropic: image input requires url or data")
	}
	if image.MIMEType == "" {
		return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("llm/anthropic: image data requires mime type")
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
		return "", "", true, fmt.Errorf("llm/anthropic: invalid image data url")
	}
	metadata := value[len("data:"):comma]
	data := value[comma+1:]
	if !strings.Contains(metadata, ";base64") {
		return "", "", true, fmt.Errorf("llm/anthropic: image data url must be base64 encoded")
	}
	mediaType := strings.Split(metadata, ";")[0]
	if mediaType == "" {
		return "", "", true, fmt.Errorf("llm/anthropic: image data url requires mime type")
	}
	return mediaType, data, true, nil
}

func convertToolResultBlock(message llm.Message) (sdkanthropic.ContentBlockParamUnion, error) {
	if len(message.Input) == 0 {
		return sdkanthropic.NewToolResultBlock(message.ToolCallID, message.Content, false), nil
	}
	content := make([]sdkanthropic.ToolResultBlockParamContentUnion, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case llm.InputPartText:
			if part.Text != "" {
				content = append(content, sdkanthropic.ToolResultBlockParamContentUnion{
					OfText: &sdkanthropic.TextBlockParam{Text: part.Text},
				})
			}
		case llm.InputPartImage:
			block, err := convertImageBlock(part.Image)
			if err != nil {
				return sdkanthropic.ContentBlockParamUnion{}, err
			}
			content = append(content, sdkanthropic.ToolResultBlockParamContentUnion{OfImage: block.OfImage})
		default:
			return sdkanthropic.ContentBlockParamUnion{}, fmt.Errorf("llm/anthropic: unsupported input part type %q", part.Type)
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

func convertAssistantBlocks(message llm.Message) ([]sdkanthropic.ContentBlockParamUnion, error) {
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

func convertTools(tools []llm.ToolSpec) []sdkanthropic.ToolUnionParam {
	result := make([]sdkanthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		param := sdkanthropic.ToolUnionParamOfTool(toAnthropicSchema(tool.InputSchema), tool.Name)
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
	required := stringSlice(schema["required"])
	return sdkanthropic.ToolInputSchemaParam{
		Properties:  schema["properties"],
		Required:    required,
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

func convertResponseContent(blocks []sdkanthropic.ContentBlockUnion) (string, []llm.ToolCall) {
	var text []string
	var calls []llm.ToolCall
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				text = append(text, block.Text)
			}
		case "tool_use":
			calls = append(calls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: json.RawMessage(bytes.Clone(block.Input)),
			})
		}
	}
	return strings.Join(text, ""), calls
}

func emitStream(ctx context.Context, handler llm.StreamHandler, chunk llm.StreamChunk) error {
	if handler == nil {
		return nil
	}
	return handler(ctx, chunk)
}

func thinkingParam(config *llm.ReasoningConfig) sdkanthropic.ThinkingConfigParamUnion {
	if config == nil {
		return sdkanthropic.ThinkingConfigParamUnion{}
	}
	if config.Enable != nil {
		if !*config.Enable {
			disabled := sdkanthropic.NewThinkingConfigDisabledParam()
			return sdkanthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
		}
		return sdkanthropic.ThinkingConfigParamUnion{
			OfAdaptive: &sdkanthropic.ThinkingConfigAdaptiveParam{
				Display: adaptiveThinkingDisplay(config.Display),
			},
		}
	}
	switch strings.ToLower(strings.TrimSpace(config.Effort)) {
	case "none":
		disabled := sdkanthropic.NewThinkingConfigDisabledParam()
		return sdkanthropic.ThinkingConfigParamUnion{OfDisabled: &disabled}
	case "low", "medium", "high", "xhigh", "max":
		return sdkanthropic.ThinkingConfigParamUnion{
			OfAdaptive: &sdkanthropic.ThinkingConfigAdaptiveParam{
				Display: adaptiveThinkingDisplay(config.Display),
			},
		}
	default:
		return sdkanthropic.ThinkingConfigParamUnion{}
	}
}

func adaptiveThinkingDisplay(value string) sdkanthropic.ThinkingConfigAdaptiveDisplay {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "omitted":
		return sdkanthropic.ThinkingConfigAdaptiveDisplayOmitted
	default:
		return sdkanthropic.ThinkingConfigAdaptiveDisplaySummarized
	}
}

func outputConfigParam(config *llm.ReasoningConfig) sdkanthropic.OutputConfigParam {
	effort := anthropicEffort(config)
	if effort == "" {
		return sdkanthropic.OutputConfigParam{}
	}
	return sdkanthropic.OutputConfigParam{
		Effort: sdkanthropic.OutputConfigEffort(effort),
	}
}

func anthropicEffort(config *llm.ReasoningConfig) string {
	if config == nil {
		return ""
	}
	switch effort := strings.ToLower(strings.TrimSpace(config.Effort)); effort {
	case "low", "medium", "high", "xhigh", "max":
		return effort
	default:
		return ""
	}
}
