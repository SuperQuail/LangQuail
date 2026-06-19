package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/superquail/langquail/llm"
	"github.com/superquail/langquail/prompt"
	"github.com/superquail/langquail/token"
	"github.com/superquail/langquail/trace"
)

func TestCompactMessagesCompactsAndPreservesUnmodifiedMessages(t *testing.T) {
	messages := []llm.Message{
		llm.System("policy"),
		llm.AssistantToolCalls("lookup", []llm.ToolCall{{
			ID:        "call_1",
			Name:      "lookup",
			Arguments: json.RawMessage(`{"q":"old"}`),
		}}),
		llm.ToolResult("call_1", `{"answer":"old"}`),
		llm.User("verbose details"),
	}
	plan := prompt.CompactPlan{Ops: []prompt.CompactOp{
		prompt.DropSegment(llm.MessageSegmentID(0)),
		prompt.ReplaceSegment(llm.MessageSegmentID(3), prompt.Segment{
			ID:      llm.MessageSegmentID(3),
			Role:    string(llm.RoleUser),
			Content: "summary",
		}),
		prompt.AddSegment(prompt.Segment{
			ID:      "manual.note",
			Role:    string(llm.RoleDeveloper),
			Content: "manual note",
		}, prompt.PositionAfterSegment(llm.MessageSegmentID(1))),
	}}

	var adjusted bool
	ctx := trace.WithEmitter(context.Background(), func(_ context.Context, eventType string, payload any) (trace.Event, error) {
		if eventType == trace.EventPromptAdjusted {
			adjusted = true
			if _, ok := payload.(prompt.CompactResult); !ok {
				t.Fatalf("payload = %#v", payload)
			}
		}
		return trace.Event{Type: eventType}, nil
	})
	compacted, result, err := llm.CompactMessages(ctx, messages, plan)
	if err != nil {
		t.Fatalf("CompactMessages() error = %v", err)
	}
	if !result.Changed || !adjusted {
		t.Fatalf("Changed=%v adjusted=%v", result.Changed, adjusted)
	}
	if len(compacted) != 4 {
		t.Fatalf("compacted = %#v", compacted)
	}
	if compacted[0].Role != llm.RoleAssistant || len(compacted[0].ToolCalls) != 1 || compacted[0].ToolCalls[0].Name != "lookup" {
		t.Fatalf("assistant message was not preserved: %#v", compacted[0])
	}
	if compacted[1].Role != llm.RoleDeveloper || compacted[1].Content != "manual note" || len(compacted[1].ToolCalls) != 0 {
		t.Fatalf("added message = %#v", compacted[1])
	}
	if compacted[2].Role != llm.RoleTool || compacted[2].ToolCallID != "call_1" {
		t.Fatalf("tool result was not preserved: %#v", compacted[2])
	}
	if compacted[3].Role != llm.RoleUser || compacted[3].Content != "summary" || compacted[3].ToolCallID != "" {
		t.Fatalf("replacement message = %#v", compacted[3])
	}

	compacted[0].ToolCalls[0].Arguments[0] = '['
	if string(messages[1].ToolCalls[0].Arguments) != `{"q":"old"}` {
		t.Fatalf("compacted message aliases original tool calls: %s", messages[1].ToolCalls[0].Arguments)
	}
}

func TestCompactMessagesEstimatesActualMessages(t *testing.T) {
	estimator := &messageCompactEstimator{}
	messages := []llm.Message{
		llm.AssistantToolCalls("lookup", []llm.ToolCall{{
			ID:        "call_1",
			Name:      "lookup",
			Arguments: json.RawMessage(`{"q":"old"}`),
		}}),
		llm.ToolResult("call_1", `{"answer":"old"}`),
		llm.User("drop me"),
	}
	compacted, result, err := llm.CompactMessages(context.Background(), messages, prompt.CompactPlan{Ops: []prompt.CompactOp{
		prompt.DropSegment(llm.MessageSegmentID(2)),
	}}, llm.WithCompactEstimator(estimator), llm.WithCompactBudget(token.Budget{
		ContextLimit:    200,
		MaxOutputTokens: 20,
	}), llm.WithCompactEstimateRequest(token.EstimateRequest{Model: "fake-model"}))
	if err != nil {
		t.Fatalf("CompactMessages() error = %v", err)
	}
	if len(compacted) != 2 {
		t.Fatalf("compacted = %#v", compacted)
	}
	if result.BeforeEstimate == nil || result.BeforeEstimate.InputTokens != 3 {
		t.Fatalf("BeforeEstimate = %#v", result.BeforeEstimate)
	}
	if result.AfterEstimate == nil || result.AfterEstimate.InputTokens != 2 {
		t.Fatalf("AfterEstimate = %#v", result.AfterEstimate)
	}
	if len(estimator.requests) != 2 {
		t.Fatalf("requests = %#v", estimator.requests)
	}
	if len(estimator.requests[0].Messages[0].ToolCalls) != 1 {
		t.Fatalf("before estimate did not use original tool calls: %#v", estimator.requests[0].Messages[0])
	}
	if estimator.requests[0].ContextLimit != 200 || estimator.requests[0].MaxOutputTokens != 20 {
		t.Fatalf("budget was not applied: %#v", estimator.requests[0])
	}
}

func TestCompactMessagesPreservesUnmodifiedImageInput(t *testing.T) {
	messages := []llm.Message{
		llm.UserInput(
			llm.InputText("look"),
			llm.InputImageData("image/png", []byte{1, 2}),
		),
		llm.User("verbose details"),
	}
	compacted, _, err := llm.CompactMessages(context.Background(), messages, prompt.CompactPlan{Ops: []prompt.CompactOp{
		prompt.ReplaceSegment(llm.MessageSegmentID(1), prompt.Segment{
			ID:      llm.MessageSegmentID(1),
			Role:    string(llm.RoleUser),
			Content: "summary",
		}),
	}})
	if err != nil {
		t.Fatalf("CompactMessages() error = %v", err)
	}
	if len(compacted) != 2 {
		t.Fatalf("compacted = %#v", compacted)
	}
	if len(compacted[0].Input) != 2 || compacted[0].Input[1].Image == nil {
		t.Fatalf("image input was not preserved: %#v", compacted[0])
	}
	if !bytes.Equal(compacted[0].Input[1].Image.Data, []byte{1, 2}) {
		t.Fatalf("image data = %v", compacted[0].Input[1].Image.Data)
	}
	compacted[0].Input[1].Image.Data[0] = 9
	if messages[0].Input[1].Image.Data[0] != 1 {
		t.Fatalf("compacted image aliases original: %v", messages[0].Input[1].Image.Data)
	}
	if len(compacted[1].Input) != 0 || compacted[1].Content != "summary" {
		t.Fatalf("replacement message = %#v", compacted[1])
	}
}

func TestCompactMessagesEstimatesImageInput(t *testing.T) {
	estimator := &messageCompactEstimator{}
	_, _, err := llm.CompactMessages(context.Background(), []llm.Message{
		llm.UserInput(
			llm.InputText("look"),
			llm.InputImageData("image/png", []byte{1, 2}),
		),
	}, prompt.CompactPlan{}, llm.WithCompactEstimator(estimator), llm.WithCompactEstimateRequest(token.EstimateRequest{Model: "fake-model"}))
	if err != nil {
		t.Fatalf("CompactMessages() error = %v", err)
	}
	if len(estimator.requests) != 2 {
		t.Fatalf("requests = %#v", estimator.requests)
	}
	message := estimator.requests[0].Messages[0]
	if len(message.Input) != 2 || message.Input[1].Image == nil {
		t.Fatalf("estimate message input = %#v", message.Input)
	}
	if message.Input[1].Image.MIMEType != "image/png" || !bytes.Equal(message.Input[1].Image.Data, []byte{1, 2}) {
		t.Fatalf("estimate image = %#v", message.Input[1].Image)
	}
}

type messageCompactEstimator struct {
	requests []token.EstimateRequest
}

func (e *messageCompactEstimator) CountPromptTokens(_ context.Context, request token.EstimateRequest) (token.Estimate, error) {
	e.requests = append(e.requests, request)
	return token.Estimate{
		Model:           request.Model,
		InputTokens:     int64(len(request.Messages)),
		ContextLimit:    request.ContextLimit,
		MaxOutputTokens: request.MaxOutputTokens,
		Source:          token.SourceTiktoken,
		Estimated:       true,
	}, nil
}
