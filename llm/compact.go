package llm

import (
	"context"
	"fmt"
	"strconv"

	lqprompt "github.com/superquail/langquail/prompt"
	lqtoken "github.com/superquail/langquail/token"
	"github.com/superquail/langquail/trace"
)

type CompactMessagesOption func(*compactMessagesConfig)

type compactMessagesConfig struct {
	estimator       lqtoken.Estimator
	budget          lqtoken.Budget
	estimateRequest lqtoken.EstimateRequest
}

func WithCompactEstimator(estimator lqtoken.Estimator) CompactMessagesOption {
	return func(config *compactMessagesConfig) {
		config.estimator = estimator
	}
}

func WithCompactBudget(budget lqtoken.Budget) CompactMessagesOption {
	return func(config *compactMessagesConfig) {
		config.budget = budget
	}
}

func WithCompactEstimateRequest(request lqtoken.EstimateRequest) CompactMessagesOption {
	return func(config *compactMessagesConfig) {
		config.estimateRequest = request
	}
}

func MessageSegmentID(index int) string {
	return fmt.Sprintf("message.%d", index)
}

func CompactMessages(ctx context.Context, messages []Message, plan lqprompt.CompactPlan, opts ...CompactMessagesOption) ([]Message, lqprompt.CompactResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config := compactMessagesConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&config)
		}
	}
	if config.estimator == nil {
		if estimator, ok := lqtoken.EstimatorFromContext(ctx); ok {
			config.estimator = estimator
		}
	}

	result, err := lqprompt.Compact(ctx, lqprompt.CompactRequest{
		Prompt: messagesPrompt(messages),
		Plan:   plan,
	})
	if err != nil {
		return nil, lqprompt.CompactResult{}, err
	}
	compacted := compactedMessages(messages, result.Prompt, result.Ops)
	before, err := estimateCompactMessages(ctx, config.estimator, config.estimateRequest, config.budget, messages)
	if err != nil {
		return nil, lqprompt.CompactResult{}, err
	}
	after, err := estimateCompactMessages(ctx, config.estimator, config.estimateRequest, config.budget, compacted)
	if err != nil {
		return nil, lqprompt.CompactResult{}, err
	}
	result.BeforeEstimate = before
	result.AfterEstimate = after
	if result.Changed {
		if _, err := trace.Emit(ctx, trace.EventPromptAdjusted, result); err != nil {
			return nil, lqprompt.CompactResult{}, err
		}
	}
	return compacted, result, nil
}

func messagesPrompt(messages []Message) lqprompt.Prompt {
	segments := make([]lqprompt.Segment, 0, len(messages))
	for i, message := range messages {
		segments = append(segments, lqprompt.Segment{
			ID:      MessageSegmentID(i),
			Role:    string(message.Role),
			Source:  "message",
			Content: message.Content,
			Metadata: map[string]string{
				"message_index": strconv.Itoa(i),
			},
		})
	}
	return lqprompt.Prompt{
		ID:       "messages",
		Segments: segments,
	}
}

func compactedMessages(original []Message, compacted lqprompt.Prompt, ops []lqprompt.CompactOp) []Message {
	originalByID := make(map[string]int, len(original))
	for i := range original {
		originalByID[MessageSegmentID(i)] = i
	}
	newIDs := compactNewSegmentIDs(ops)
	messages := make([]Message, 0, len(compacted.Segments))
	for _, segment := range compacted.Segments {
		if index, ok := originalByID[segment.ID]; ok {
			if _, replaced := newIDs[segment.ID]; !replaced {
				messages = append(messages, cloneMessage(original[index]))
				continue
			}
		}
		messages = append(messages, Message{
			Role:    Role(segment.Role),
			Content: segment.Content,
		})
	}
	return messages
}

func compactNewSegmentIDs(ops []lqprompt.CompactOp) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, op := range ops {
		switch op.Action {
		case lqprompt.CompactReplace, lqprompt.CompactAdd:
			if op.Replacement != nil && op.Replacement.ID != "" {
				ids[op.Replacement.ID] = struct{}{}
			}
		}
	}
	return ids
}

func estimateCompactMessages(ctx context.Context, estimator lqtoken.Estimator, request lqtoken.EstimateRequest, budget lqtoken.Budget, messages []Message) (*lqtoken.Estimate, error) {
	if estimator == nil {
		return nil, nil
	}
	if budget.ContextLimit > 0 {
		request.ContextLimit = budget.ContextLimit
	}
	if budget.MaxOutputTokens > 0 {
		request.MaxOutputTokens = budget.MaxOutputTokens
	}
	request.Messages = toTokenMessages(messages)
	estimate, err := estimator.CountPromptTokens(ctx, request)
	if err != nil {
		return nil, err
	}
	return &estimate, nil
}

func cloneMessage(message Message) Message {
	message.ToolCalls = cloneToolCalls(message.ToolCalls)
	return message
}
