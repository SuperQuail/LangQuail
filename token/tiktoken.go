package token

import (
	"context"
	"encoding/json"
	"fmt"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

type TiktokenEstimator struct {
	Encoding string
}

func NewTiktokenEstimator() TiktokenEstimator {
	return TiktokenEstimator{}
}

func (e TiktokenEstimator) CountPromptTokens(_ context.Context, request EstimateRequest) (Estimate, error) {
	if err := ValidateRequest(request); err != nil {
		return Estimate{}, err
	}
	encoding, err := e.encodingFor(request.Model)
	if err != nil {
		return Estimate{}, err
	}
	var systemTokens int64
	var messageTokens int64
	for _, message := range request.Messages {
		count := int64(3)
		count += countText(encoding, message.Role)
		count += countText(encoding, message.Name)
		if len(message.Input) > 0 {
			count += countInputParts(encoding, message.Input)
		} else {
			count += countText(encoding, message.Content)
		}
		count += countText(encoding, message.ToolCallID)
		for _, call := range message.ToolCalls {
			count += countText(encoding, call.ID)
			count += countText(encoding, call.Name)
			count += countText(encoding, string(call.Arguments))
		}
		if message.Name != "" {
			count++
		}
		if message.Role == "system" || message.Role == "developer" {
			systemTokens += count
		}
		messageTokens += count
	}
	var toolTokens int64
	for _, tool := range request.Tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			return Estimate{}, fmt.Errorf("token: marshal tool spec: %w", err)
		}
		toolTokens += countText(encoding, string(raw))
	}
	overhead := int64(3)
	input := messageTokens + toolTokens + overhead
	return finalizeEstimate(Estimate{
		Provider:         request.Provider,
		Model:            request.Model,
		InputTokens:      input,
		SystemTokens:     systemTokens,
		MessageTokens:    messageTokens,
		ToolSchemaTokens: toolTokens,
		OverheadTokens:   overhead,
		ContextLimit:     request.ContextLimit,
		MaxOutputTokens:  request.MaxOutputTokens,
		Source:           SourceTiktoken,
		Estimated:        true,
	}), nil
}

func (e TiktokenEstimator) encodingFor(model string) (*tiktoken.Tiktoken, error) {
	if e.Encoding != "" {
		return tiktoken.GetEncoding(e.Encoding)
	}
	encoding, err := tiktoken.EncodingForModel(model)
	if err == nil {
		return encoding, nil
	}
	return tiktoken.GetEncoding("cl100k_base")
}

func countText(encoding *tiktoken.Tiktoken, text string) int64 {
	if text == "" {
		return 0
	}
	return int64(len(encoding.Encode(text, nil, nil)))
}

func countInputParts(encoding *tiktoken.Tiktoken, parts []InputPart) int64 {
	var count int64
	for _, part := range parts {
		count += countText(encoding, string(part.Type))
		switch part.Type {
		case InputPartText:
			count += countText(encoding, part.Text)
		case InputPartImage:
			count += countText(encoding, "image")
			if part.Image != nil {
				count += countText(encoding, part.Image.URL)
				count += countText(encoding, part.Image.MIMEType)
			}
		default:
			count += countText(encoding, part.Text)
		}
	}
	return count
}
