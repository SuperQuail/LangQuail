package llm

import (
	"bytes"
	"encoding/json"
	"strings"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role        `json:"role"`
	Content    string      `json:"content,omitempty"`
	Input      []InputPart `json:"input,omitempty"`
	Name       string      `json:"name,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
}

type InputPartType string

const (
	InputPartText  InputPartType = "input_text"
	InputPartImage InputPartType = "input_image"
)

type InputPart struct {
	Type  InputPartType `json:"type"`
	Text  string        `json:"text,omitempty"`
	Image *InputImage   `json:"image,omitempty"`
}

type InputImage struct {
	URL      string `json:"url,omitempty"`
	Data     []byte `json:"data,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

func System(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

func Developer(content string) Message {
	return Message{Role: RoleDeveloper, Content: content}
}

func User(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

func UserInput(parts ...InputPart) Message {
	input := cloneInputParts(parts)
	return Message{
		Role:    RoleUser,
		Content: inputTextContent(input),
		Input:   input,
	}
}

func Assistant(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

func AssistantInput(parts ...InputPart) Message {
	input := cloneInputParts(parts)
	return Message{
		Role:    RoleAssistant,
		Content: inputTextContent(input),
		Input:   input,
	}
}

func AssistantToolCalls(content string, calls []ToolCall) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: cloneToolCalls(calls)}
}

func ToolResult(toolCallID string, content string) Message {
	return Message{Role: RoleTool, ToolCallID: toolCallID, Content: content}
}

func InputText(text string) InputPart {
	return InputPart{Type: InputPartText, Text: text}
}

func InputImageURL(url string) InputPart {
	return InputPart{Type: InputPartImage, Image: &InputImage{URL: url}}
}

func InputImageData(mimeType string, data []byte) InputPart {
	return InputPart{
		Type: InputPartImage,
		Image: &InputImage{
			Data:     bytes.Clone(data),
			MIMEType: mimeType,
		},
	}
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	cloned := make([]ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = call
		cloned[i].Arguments = json.RawMessage(bytes.Clone(call.Arguments))
	}
	return cloned
}

func cloneInputParts(parts []InputPart) []InputPart {
	if len(parts) == 0 {
		return nil
	}
	cloned := make([]InputPart, len(parts))
	for i, part := range parts {
		cloned[i] = part
		if part.Image != nil {
			image := *part.Image
			image.Data = bytes.Clone(part.Image.Data)
			cloned[i].Image = &image
		}
	}
	return cloned
}

func inputTextContent(parts []InputPart) string {
	if len(parts) == 0 {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == InputPartText && part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
