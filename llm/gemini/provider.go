package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/genai"

	"github.com/superquail/langquail/llm"
)

const maxInt32 = int64(1<<31 - 1)

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
	client, err := p.newClient(ctx)
	if err != nil {
		return llm.Response{}, err
	}
	contents, config, err := convertRequest(request)
	if err != nil {
		return llm.Response{}, err
	}
	response, err := client.Models.GenerateContent(ctx, request.Model, contents, config)
	if err != nil {
		return llm.Response{}, err
	}
	return convertResponse(response, request.Model, true)
}

func (p *ProviderAdapter) ChatStream(ctx context.Context, request llm.Request, handler llm.StreamHandler) (llm.Response, error) {
	client, err := p.newClient(ctx)
	if err != nil {
		return llm.Response{}, err
	}
	contents, config, err := convertRequest(request)
	if err != nil {
		return llm.Response{}, err
	}

	var text strings.Builder
	var responseID string
	model := request.Model
	var stopReason string
	var usage llm.Usage
	var calls []llm.ToolCall
	var rawChunks []json.RawMessage

	for chunk, err := range client.Models.GenerateContentStream(ctx, request.Model, contents, config) {
		if err != nil {
			return llm.Response{}, err
		}
		if chunk == nil {
			continue
		}
		if raw := rawJSON(chunk); len(raw) > 0 {
			rawChunks = append(rawChunks, raw)
		}
		if chunk.ResponseID != "" {
			responseID = chunk.ResponseID
		}
		if chunk.ModelVersion != "" {
			model = chunk.ModelVersion
		}
		if chunk.UsageMetadata != nil {
			usage = convertUsage(chunk.UsageMetadata)
			if usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 {
				if err := emitStream(ctx, handler, llm.StreamChunk{Usage: &usage}); err != nil {
					return llm.Response{}, err
				}
			}
		}
		for _, candidate := range chunk.Candidates {
			if candidate == nil {
				continue
			}
			if candidate.FinishReason != "" {
				stopReason = string(candidate.FinishReason)
			}
			for _, part := range candidateParts(candidate) {
				if part.Text != "" {
					if part.Thought {
						if err := emitStream(ctx, handler, llm.StreamChunk{Thinking: part.Text}); err != nil {
							return llm.Response{}, err
						}
						continue
					}
					text.WriteString(part.Text)
					if err := emitStream(ctx, handler, llm.StreamChunk{Text: part.Text}); err != nil {
						return llm.Response{}, err
					}
				}
				if part.FunctionCall != nil {
					calls = append(calls, convertFunctionCall(part.FunctionCall))
				}
			}
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
		ID:         responseID,
		Model:      model,
		Message:    llm.AssistantToolCalls(text.String(), calls),
		Text:       text.String(),
		ToolCalls:  calls,
		Usage:      usage,
		StopReason: stopReason,
		Raw:        streamRaw(rawChunks),
	}, nil
}

func (p *ProviderAdapter) newClient(ctx context.Context) (*genai.Client, error) {
	if p == nil {
		return nil, fmt.Errorf("llm/gemini: nil provider")
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("llm/gemini: api key is required")
	}
	config := &genai.ClientConfig{
		APIKey:  p.apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if p.baseURL != "" {
		config.HTTPOptions = genai.HTTPOptions{BaseURL: p.baseURL}
	}
	return genai.NewClient(ctx, config)
}

func convertRequest(request llm.Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	contents, system, err := convertMessages(request.Messages)
	if err != nil {
		return nil, nil, err
	}
	config := &genai.GenerateContentConfig{}
	hasConfig := false
	if system != nil {
		config.SystemInstruction = system
		hasConfig = true
	}
	if request.Temperature != nil {
		temperature := float32(*request.Temperature)
		config.Temperature = &temperature
		hasConfig = true
	}
	if request.MaxTokens > 0 {
		if request.MaxTokens > maxInt32 {
			return nil, nil, fmt.Errorf("llm/gemini: max_tokens exceeds %d", maxInt32)
		}
		config.MaxOutputTokens = int32(request.MaxTokens)
		hasConfig = true
	}
	if tools := convertTools(request.Tools); len(tools) > 0 {
		config.Tools = tools
		hasConfig = true
	}
	if request.ToolChoice != "" {
		toolConfig, err := convertToolChoice(request.ToolChoice, request.Tools)
		if err != nil {
			return nil, nil, err
		}
		config.ToolConfig = toolConfig
		hasConfig = true
	}
	if !hasConfig {
		config = nil
	}
	return contents, config, nil
}

func convertMessages(messages []llm.Message) ([]*genai.Content, *genai.Content, error) {
	contents := make([]*genai.Content, 0, len(messages))
	var systemParts []*genai.Part
	toolNames := make(map[string]string)

	for _, message := range messages {
		switch message.Role {
		case llm.RoleSystem, llm.RoleDeveloper:
			if message.Content != "" {
				systemParts = append(systemParts, genai.NewPartFromText(message.Content))
			}
		case llm.RoleAssistant:
			parts := make([]*genai.Part, 0, len(message.ToolCalls)+1)
			if message.Content != "" {
				parts = append(parts, genai.NewPartFromText(message.Content))
			}
			for _, call := range message.ToolCalls {
				toolNames[call.ID] = call.Name
				parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
					ID:   call.ID,
					Name: call.Name,
					Args: decodeObjectOrRaw(call.Arguments),
				}})
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: genai.RoleModel, Parts: parts})
			}
		case llm.RoleTool:
			name := message.Name
			if name == "" {
				name = toolNames[message.ToolCallID]
			}
			if name == "" {
				return nil, nil, fmt.Errorf("llm/gemini: tool result %q has no matching tool call name", message.ToolCallID)
			}
			contents = append(contents, &genai.Content{
				Role: genai.RoleUser,
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       message.ToolCallID,
						Name:     name,
						Response: decodeToolResponse(message.Content),
					},
				}},
			})
		case llm.RoleUser:
			if message.Content != "" {
				contents = append(contents, genai.NewContentFromText(message.Content, genai.RoleUser))
			}
		default:
			if message.Content != "" {
				contents = append(contents, genai.NewContentFromText(message.Content, genai.RoleUser))
			}
		}
	}
	if len(systemParts) == 0 {
		return contents, nil, nil
	}
	return contents, &genai.Content{Parts: systemParts}, nil
}

func decodeObjectOrRaw(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil && object != nil {
		return object
	}
	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		return map[string]any{"value": value}
	}
	return map[string]any{"raw": string(raw)}
}

func decodeToolResponse(content string) map[string]any {
	var object map[string]any
	if err := json.Unmarshal([]byte(content), &object); err == nil && object != nil {
		return object
	}
	return map[string]any{"output": content}
}

func convertTools(tools []llm.ToolSpec) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}
	declarations := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		declarations = append(declarations, &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schemaFromRaw(tool.InputSchema),
		})
	}
	return []*genai.Tool{{FunctionDeclarations: declarations}}
}

func convertToolChoice(choice llm.ToolChoice, tools []llm.ToolSpec) (*genai.ToolConfig, error) {
	config := &genai.FunctionCallingConfig{}
	switch choice {
	case llm.ToolChoiceAuto:
		config.Mode = genai.FunctionCallingConfigModeAuto
	case llm.ToolChoiceNone:
		config.Mode = genai.FunctionCallingConfigModeNone
	case llm.ToolChoiceRequired:
		config.Mode = genai.FunctionCallingConfigModeAny
		config.AllowedFunctionNames = toolNames(tools)
	default:
		return nil, fmt.Errorf("llm/gemini: unsupported tool_choice %q", choice)
	}
	return &genai.ToolConfig{FunctionCallingConfig: config}, nil
}

func toolNames(tools []llm.ToolSpec) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	return names
}

func convertResponse(response *genai.GenerateContentResponse, fallbackModel string, requireCandidate bool) (llm.Response, error) {
	if response == nil {
		return llm.Response{}, fmt.Errorf("llm/gemini: empty response")
	}
	if requireCandidate && len(response.Candidates) == 0 {
		return llm.Response{}, fmt.Errorf("llm/gemini: empty response candidates")
	}
	text, calls, stopReason := responseContent(response)
	model := response.ModelVersion
	if model == "" {
		model = fallbackModel
	}
	return llm.Response{
		ID:         response.ResponseID,
		Model:      model,
		Message:    llm.AssistantToolCalls(text, calls),
		Text:       text,
		ToolCalls:  calls,
		Usage:      convertUsage(response.UsageMetadata),
		StopReason: stopReason,
		Raw:        rawJSON(response),
	}, nil
}

func responseContent(response *genai.GenerateContentResponse) (string, []llm.ToolCall, string) {
	if response == nil || len(response.Candidates) == 0 || response.Candidates[0] == nil {
		return "", nil, ""
	}
	candidate := response.Candidates[0]
	var text strings.Builder
	var calls []llm.ToolCall
	for _, part := range candidateParts(candidate) {
		if part.Text != "" && !part.Thought {
			text.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			calls = append(calls, convertFunctionCall(part.FunctionCall))
		}
	}
	return text.String(), calls, string(candidate.FinishReason)
}

func candidateParts(candidate *genai.Candidate) []*genai.Part {
	if candidate == nil || candidate.Content == nil {
		return nil
	}
	return candidate.Content.Parts
}

func convertFunctionCall(call *genai.FunctionCall) llm.ToolCall {
	arguments := json.RawMessage(`{}`)
	if call != nil && call.Args != nil {
		if data, err := json.Marshal(call.Args); err == nil {
			arguments = json.RawMessage(data)
		}
	}
	if call == nil {
		return llm.ToolCall{Arguments: arguments}
	}
	return llm.ToolCall{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: arguments,
	}
}

func convertUsage(metadata *genai.GenerateContentResponseUsageMetadata) llm.Usage {
	if metadata == nil {
		return llm.Usage{}
	}
	prompt := int64(metadata.PromptTokenCount)
	cached := int64(metadata.CachedContentTokenCount)
	toolUse := int64(metadata.ToolUsePromptTokenCount)
	candidates := int64(metadata.CandidatesTokenCount)
	thoughts := int64(metadata.ThoughtsTokenCount)
	total := int64(metadata.TotalTokenCount)
	if total == 0 {
		total = prompt + toolUse + candidates + thoughts
	}
	return llm.Usage{
		InputTokens:         prompt + toolUse,
		InputUncachedTokens: nonNegative(prompt-cached) + toolUse,
		InputCachedTokens:   cached,
		OutputTokens:        candidates,
		ReasoningTokens:     thoughts,
		TotalTokens:         total,
	}
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func emitStream(ctx context.Context, handler llm.StreamHandler, chunk llm.StreamChunk) error {
	if handler == nil {
		return nil
	}
	return handler(ctx, chunk)
}

func rawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil || len(data) == 0 {
		return nil
	}
	return json.RawMessage(data)
}

func streamRaw(chunks []json.RawMessage) json.RawMessage {
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	return rawJSON(chunks)
}

func schemaFromRaw(raw json.RawMessage) *genai.Schema {
	if len(raw) == 0 {
		return defaultObjectSchema()
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return defaultObjectSchema()
	}
	schema := schemaFromValue(decoded)
	if schema == nil {
		return defaultObjectSchema()
	}
	return schema
}

func defaultObjectSchema() *genai.Schema {
	return &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}}
}

func schemaFromValue(value any) *genai.Schema {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	schema := &genai.Schema{}
	if typ, nullable := schemaType(object["type"]); typ != "" {
		schema.Type = typ
		if nullable {
			schema.Nullable = boolPtr(true)
		}
	}
	if description, ok := object["description"].(string); ok {
		schema.Description = description
	}
	if title, ok := object["title"].(string); ok {
		schema.Title = title
	}
	if format, ok := object["format"].(string); ok {
		schema.Format = format
	}
	if pattern, ok := object["pattern"].(string); ok {
		schema.Pattern = pattern
	}
	if nullable, ok := object["nullable"].(bool); ok {
		schema.Nullable = boolPtr(nullable)
	}
	if value, exists := object["default"]; exists {
		schema.Default = value
	}
	if value, exists := object["example"]; exists {
		schema.Example = value
	}
	if enum := stringValues(object["enum"]); len(enum) > 0 {
		schema.Enum = enum
	}
	if required := stringValues(object["required"]); len(required) > 0 {
		schema.Required = required
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		schema.Properties = make(map[string]*genai.Schema, len(properties))
		names := make([]string, 0, len(properties))
		for name, property := range properties {
			child := schemaFromValue(property)
			if child == nil {
				child = defaultObjectSchema()
			}
			schema.Properties[name] = child
			names = append(names, name)
		}
		if schema.Type == "" {
			schema.Type = genai.TypeObject
		}
		if ordering := stringValues(object["propertyOrdering"]); len(ordering) > 0 {
			schema.PropertyOrdering = ordering
		} else {
			sort.Strings(names)
			schema.PropertyOrdering = names
		}
	}
	if items := schemaFromValue(object["items"]); items != nil {
		schema.Items = items
		if schema.Type == "" {
			schema.Type = genai.TypeArray
		}
	}
	if anyOf := schemaList(object["anyOf"]); len(anyOf) > 0 {
		schema.AnyOf = anyOf
	}
	if schema.AnyOf == nil {
		if oneOf := schemaList(object["oneOf"]); len(oneOf) > 0 {
			schema.AnyOf = oneOf
		}
	}
	schema.MinItems = int64Ptr(object["minItems"])
	schema.MaxItems = int64Ptr(object["maxItems"])
	schema.MinLength = int64Ptr(object["minLength"])
	schema.MaxLength = int64Ptr(object["maxLength"])
	schema.MinProperties = int64Ptr(object["minProperties"])
	schema.MaxProperties = int64Ptr(object["maxProperties"])
	schema.Minimum = float64Ptr(object["minimum"])
	schema.Maximum = float64Ptr(object["maximum"])
	if schema.Type == "" && len(schema.AnyOf) == 0 {
		schema.Type = genai.TypeObject
	}
	return schema
}

func schemaType(value any) (genai.Type, bool) {
	switch typed := value.(type) {
	case string:
		return scalarSchemaType(typed), false
	case []any:
		nullable := false
		var result genai.Type
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			if strings.EqualFold(text, "null") {
				nullable = true
				continue
			}
			if result == "" {
				result = scalarSchemaType(text)
			}
		}
		return result, nullable
	default:
		return "", false
	}
}

func scalarSchemaType(value string) genai.Type {
	switch strings.ToLower(value) {
	case "object":
		return genai.TypeObject
	case "string":
		return genai.TypeString
	case "number":
		return genai.TypeNumber
	case "integer":
		return genai.TypeInteger
	case "boolean":
		return genai.TypeBoolean
	case "array":
		return genai.TypeArray
	case "null":
		return genai.TypeNULL
	default:
		return genai.Type(value)
	}
}

func schemaList(value any) []*genai.Schema {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]*genai.Schema, 0, len(items))
	for _, item := range items {
		if schema := schemaFromValue(item); schema != nil {
			result = append(result, schema)
		}
	}
	return result
}

func stringValues(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			result = append(result, typed)
		case float64, bool:
			result = append(result, fmt.Sprint(typed))
		}
	}
	return result
}

func int64Ptr(value any) *int64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	converted := int64(number)
	return &converted
}

func float64Ptr(value any) *float64 {
	number, ok := value.(float64)
	if !ok {
		return nil
	}
	return &number
}

func boolPtr(value bool) *bool {
	return &value
}
