package gemini

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/genai"

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
		return token.Estimate{}, fmt.Errorf("token/gemini: nil estimator")
	}
	if err := token.ValidateRequest(request); err != nil {
		return token.Estimate{}, err
	}
	if e.apiKey == "" {
		return token.Estimate{}, fmt.Errorf("token/gemini: api key is required")
	}
	client, err := e.newClient(ctx)
	if err != nil {
		return token.Estimate{}, err
	}
	contents, system, err := convertMessages(request.Messages)
	if err != nil {
		return token.Estimate{}, err
	}
	config := countTokensConfig(contents, system, request.Tools)
	count, err := client.Models.CountTokens(ctx, request.Model, nil, config)
	if err != nil {
		return token.Estimate{}, err
	}
	return finalize(token.Estimate{
		Provider:        request.Provider,
		Model:           request.Model,
		InputTokens:     int64(count.TotalTokens),
		ContextLimit:    request.ContextLimit,
		MaxOutputTokens: request.MaxOutputTokens,
		Source:          token.SourceGeminiAPI,
		Estimated:       true,
	}), nil
}

func (e *Estimator) newClient(ctx context.Context) (*genai.Client, error) {
	config := &genai.ClientConfig{
		APIKey:  e.apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if e.baseURL != "" {
		config.HTTPOptions = genai.HTTPOptions{BaseURL: e.baseURL}
	}
	return genai.NewClient(ctx, config)
}

func countTokensConfig(contents []*genai.Content, system *genai.Content, tools []token.ToolSpec) *genai.CountTokensConfig {
	generateRequest := map[string]any{}
	if len(contents) > 0 {
		generateRequest["contents"] = plain(contents)
	}
	if system != nil {
		generateRequest["systemInstruction"] = plain(system)
	}
	if convertedTools := convertTools(tools); len(convertedTools) > 0 {
		generateRequest["tools"] = plain(convertedTools)
	}
	return &genai.CountTokensConfig{
		HTTPOptions: &genai.HTTPOptions{
			ExtraBody: map[string]any{
				"generateContentRequest": generateRequest,
			},
		},
	}
}

func plain(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return decoded
}

func convertMessages(messages []token.Message) ([]*genai.Content, *genai.Content, error) {
	contents := make([]*genai.Content, 0, len(messages))
	var systemParts []*genai.Part
	toolNames := make(map[string]string)

	for _, message := range messages {
		switch message.Role {
		case "system", "developer":
			parts, err := convertMessageParts(message)
			if err != nil {
				return nil, nil, err
			}
			systemParts = append(systemParts, parts...)
		case "assistant":
			parts, err := convertMessageParts(message)
			if err != nil {
				return nil, nil, err
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
		case "tool":
			if hasImageInput(message) {
				return nil, nil, fmt.Errorf("token/gemini: image parts cannot be encoded in tool result messages")
			}
			name := message.Name
			if name == "" {
				name = toolNames[message.ToolCallID]
			}
			if name == "" {
				return nil, nil, fmt.Errorf("token/gemini: tool result %q has no matching tool call name", message.ToolCallID)
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
		default:
			parts, err := convertMessageParts(message)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) > 0 {
				contents = append(contents, &genai.Content{Role: genai.RoleUser, Parts: parts})
			}
		}
	}
	if len(systemParts) == 0 {
		return contents, nil, nil
	}
	return contents, &genai.Content{Parts: systemParts}, nil
}

func convertMessageParts(message token.Message) ([]*genai.Part, error) {
	if len(message.Input) == 0 {
		if message.Content == "" {
			return nil, nil
		}
		return []*genai.Part{genai.NewPartFromText(message.Content)}, nil
	}
	parts := make([]*genai.Part, 0, len(message.Input))
	for _, part := range message.Input {
		switch part.Type {
		case token.InputPartText:
			if part.Text != "" {
				parts = append(parts, genai.NewPartFromText(part.Text))
			}
		case token.InputPartImage:
			converted, err := convertImagePart(part.Image)
			if err != nil {
				return nil, err
			}
			parts = append(parts, converted)
		default:
			return nil, fmt.Errorf("token/gemini: unsupported input part type %q", part.Type)
		}
	}
	if len(parts) == 0 && message.Content != "" {
		parts = append(parts, genai.NewPartFromText(message.Content))
	}
	return parts, nil
}

func convertImagePart(image *token.InputImage) (*genai.Part, error) {
	if image == nil {
		return nil, fmt.Errorf("token/gemini: image input is missing image data")
	}
	if image.URL != "" {
		if mimeType, data, ok, err := parseImageDataURL(image.URL); err != nil {
			return nil, err
		} else if ok {
			return genai.NewPartFromBytes(data, mimeType), nil
		}
		if image.MIMEType == "" {
			return nil, fmt.Errorf("token/gemini: image url requires mime type")
		}
		return genai.NewPartFromURI(image.URL, image.MIMEType), nil
	}
	if len(image.Data) == 0 {
		return nil, fmt.Errorf("token/gemini: image input requires url or data")
	}
	if image.MIMEType == "" {
		return nil, fmt.Errorf("token/gemini: image data requires mime type")
	}
	return genai.NewPartFromBytes(image.Data, image.MIMEType), nil
}

func parseImageDataURL(value string) (string, []byte, bool, error) {
	if !strings.HasPrefix(value, "data:") {
		return "", nil, false, nil
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 {
		return "", nil, true, fmt.Errorf("token/gemini: invalid image data url")
	}
	metadata := value[len("data:"):comma]
	if !strings.Contains(metadata, ";base64") {
		return "", nil, true, fmt.Errorf("token/gemini: image data url must be base64 encoded")
	}
	mediaType := strings.Split(metadata, ";")[0]
	if mediaType == "" {
		return "", nil, true, fmt.Errorf("token/gemini: image data url requires mime type")
	}
	data, err := base64.StdEncoding.DecodeString(value[comma+1:])
	if err != nil {
		return "", nil, true, fmt.Errorf("token/gemini: decode image data url: %w", err)
	}
	return mediaType, data, true, nil
}

func hasImageInput(message token.Message) bool {
	for _, part := range message.Input {
		if part.Type == token.InputPartImage {
			return true
		}
	}
	return false
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

func convertTools(tools []token.ToolSpec) []*genai.Tool {
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

func finalize(estimate token.Estimate) token.Estimate {
	if estimate.ContextLimit > 0 {
		estimate.RemainingTokens = estimate.ContextLimit - estimate.InputTokens - estimate.MaxOutputTokens
	}
	return estimate
}
