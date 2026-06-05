package prompt

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/superquail/langquail/token"
)

type CompactAction string

const (
	CompactKeep    CompactAction = "keep"
	CompactDrop    CompactAction = "drop"
	CompactReplace CompactAction = "replace"
	CompactAdd     CompactAction = "add"
)

type CompactPositionKind string

const (
	CompactPositionStart  CompactPositionKind = "start"
	CompactPositionEnd    CompactPositionKind = "end"
	CompactPositionIndex  CompactPositionKind = "index"
	CompactPositionBefore CompactPositionKind = "before"
	CompactPositionAfter  CompactPositionKind = "after"
)

type CompactPosition struct {
	Kind      CompactPositionKind `json:"kind"`
	SegmentID string              `json:"segment_id,omitempty"`
	Index     *int                `json:"index,omitempty"`
}

type CompactOp struct {
	Action      CompactAction   `json:"action"`
	SegmentID   string          `json:"segment_id,omitempty"`
	Replacement *Segment        `json:"replacement,omitempty"`
	Position    CompactPosition `json:"position,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

type CompactPlan struct {
	Ops []CompactOp `json:"ops"`
}

type CompactRequest struct {
	Prompt          Prompt                `json:"prompt"`
	Plan            CompactPlan           `json:"plan"`
	Estimator       token.Estimator       `json:"-"`
	Budget          token.Budget          `json:"budget,omitempty"`
	EstimateRequest token.EstimateRequest `json:"estimate_request,omitempty"`
}

type CompactResult struct {
	Prompt         Prompt          `json:"prompt"`
	BeforeEstimate *token.Estimate `json:"before_estimate,omitempty"`
	AfterEstimate  *token.Estimate `json:"after_estimate,omitempty"`
	Ops            []CompactOp     `json:"ops,omitempty"`
	Changed        bool            `json:"changed"`
}

func PositionStart() CompactPosition {
	return CompactPosition{Kind: CompactPositionStart}
}

func PositionEnd() CompactPosition {
	return CompactPosition{Kind: CompactPositionEnd}
}

func PositionIndex(index int) CompactPosition {
	return CompactPosition{Kind: CompactPositionIndex, Index: intPtr(index)}
}

func PositionBeforeSegment(id string) CompactPosition {
	return CompactPosition{Kind: CompactPositionBefore, SegmentID: id}
}

func PositionAfterSegment(id string) CompactPosition {
	return CompactPosition{Kind: CompactPositionAfter, SegmentID: id}
}

func PositionBeforeIndex(index int) CompactPosition {
	return CompactPosition{Kind: CompactPositionBefore, Index: intPtr(index)}
}

func PositionAfterIndex(index int) CompactPosition {
	return CompactPosition{Kind: CompactPositionAfter, Index: intPtr(index)}
}

func KeepSegment(id string) CompactOp {
	return CompactOp{Action: CompactKeep, SegmentID: id}
}

func DropSegment(id string) CompactOp {
	return CompactOp{Action: CompactDrop, SegmentID: id}
}

func ReplaceSegment(id string, replacement Segment) CompactOp {
	return CompactOp{Action: CompactReplace, SegmentID: id, Replacement: cloneSegmentPtr(replacement)}
}

func AddSegment(replacement Segment, position CompactPosition) CompactOp {
	return CompactOp{Action: CompactAdd, Replacement: cloneSegmentPtr(replacement), Position: position}
}

func Compact(ctx context.Context, request CompactRequest) (CompactResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return CompactResult{}, ctx.Err()
	default:
	}

	original := clonePrompt(request.Prompt)
	segmentIndex, err := indexSegments(original.Segments)
	if err != nil {
		return CompactResult{}, err
	}

	mutations := make(map[string]CompactOp)
	explicitKeeps := make(map[string]struct{})
	var adds []CompactOp
	for _, op := range request.Plan.Ops {
		switch op.Action {
		case CompactKeep:
			if err := validateTarget(segmentIndex, op.SegmentID, op.Action); err != nil {
				return CompactResult{}, err
			}
			explicitKeeps[op.SegmentID] = struct{}{}
		case CompactDrop, CompactReplace:
			if err := validateTarget(segmentIndex, op.SegmentID, op.Action); err != nil {
				return CompactResult{}, err
			}
			if _, kept := explicitKeeps[op.SegmentID]; kept {
				return CompactResult{}, fmt.Errorf("prompt: segment %q has conflicting keep and %s operations", op.SegmentID, op.Action)
			}
			if _, exists := mutations[op.SegmentID]; exists {
				return CompactResult{}, fmt.Errorf("prompt: segment %q is modified more than once", op.SegmentID)
			}
			if op.Action == CompactReplace {
				if op.Replacement == nil {
					return CompactResult{}, fmt.Errorf("prompt: replace operation for segment %q requires replacement", op.SegmentID)
				}
				if err := validateReplacement(*op.Replacement, op.Action); err != nil {
					return CompactResult{}, err
				}
			}
			mutations[op.SegmentID] = op
		case CompactAdd:
			if op.Replacement == nil {
				return CompactResult{}, errors.New("prompt: add operation requires replacement")
			}
			if err := validateReplacement(*op.Replacement, op.Action); err != nil {
				return CompactResult{}, err
			}
			if err := validatePositionShape(op.Position); err != nil {
				return CompactResult{}, err
			}
			adds = append(adds, op)
		default:
			return CompactResult{}, fmt.Errorf("prompt: unsupported compact action %q", op.Action)
		}
	}

	base := make([]Segment, 0, len(original.Segments))
	for _, segment := range original.Segments {
		op, mutated := mutations[segment.ID]
		if !mutated {
			base = append(base, cloneSegment(segment))
			continue
		}
		switch op.Action {
		case CompactDrop:
			continue
		case CompactReplace:
			base = append(base, cloneSegment(*op.Replacement))
		}
	}
	if _, err := indexSegments(base); err != nil {
		return CompactResult{}, err
	}

	compacted := base
	for _, op := range adds {
		slot, err := resolvePosition(compacted, op.Position)
		if err != nil {
			return CompactResult{}, err
		}
		replacement := cloneSegment(*op.Replacement)
		compacted = insertSegment(compacted, slot, replacement)
		if _, err := indexSegments(compacted); err != nil {
			return CompactResult{}, err
		}
	}

	next := Prompt{
		ID:       original.ID,
		Segments: compacted,
		Metadata: cloneMetadata(original.Metadata),
	}
	if _, err := indexSegments(next.Segments); err != nil {
		return CompactResult{}, err
	}
	before, err := estimatePrompt(ctx, request.Estimator, request.EstimateRequest, request.Budget, original)
	if err != nil {
		return CompactResult{}, err
	}
	after, err := estimatePrompt(ctx, request.Estimator, request.EstimateRequest, request.Budget, next)
	if err != nil {
		return CompactResult{}, err
	}

	return CompactResult{
		Prompt:         next,
		BeforeEstimate: before,
		AfterEstimate:  after,
		Ops:            cloneOps(request.Plan.Ops),
		Changed:        !reflect.DeepEqual(original, next),
	}, nil
}

func validateTarget(segments map[string]int, id string, action CompactAction) error {
	if id == "" {
		return fmt.Errorf("prompt: %s operation requires segment id", action)
	}
	if _, exists := segments[id]; !exists {
		return fmt.Errorf("prompt: segment %q is not found", id)
	}
	return nil
}

func validateReplacement(segment Segment, action CompactAction) error {
	if segment.ID == "" {
		return fmt.Errorf("prompt: %s replacement requires segment id", action)
	}
	return nil
}

func validatePositionShape(position CompactPosition) error {
	hasSegment := position.SegmentID != ""
	hasIndex := position.Index != nil
	switch position.Kind {
	case CompactPositionStart, CompactPositionEnd:
		if hasSegment || hasIndex {
			return fmt.Errorf("prompt: %s position does not accept segment id or index", position.Kind)
		}
	case CompactPositionIndex:
		if !hasIndex {
			return errors.New("prompt: index position requires index")
		}
		if hasSegment {
			return errors.New("prompt: index position does not accept segment id")
		}
	case CompactPositionBefore, CompactPositionAfter:
		if hasSegment == hasIndex {
			return fmt.Errorf("prompt: %s position requires exactly one of segment id or index", position.Kind)
		}
	default:
		return fmt.Errorf("prompt: unsupported compact position %q", position.Kind)
	}
	return nil
}

func resolvePosition(segments []Segment, position CompactPosition) (int, error) {
	length := len(segments)
	switch position.Kind {
	case CompactPositionStart:
		return 0, nil
	case CompactPositionEnd:
		return length, nil
	case CompactPositionIndex:
		index := *position.Index
		if index < 0 || index > length {
			return 0, fmt.Errorf("prompt: index position %d out of range 0..%d", index, length)
		}
		return index, nil
	case CompactPositionBefore:
		if position.SegmentID != "" {
			index, ok := findSegment(segments, position.SegmentID)
			if !ok {
				return 0, fmt.Errorf("prompt: position segment %q is not found", position.SegmentID)
			}
			return index, nil
		}
		return clamp(*position.Index, 0, length), nil
	case CompactPositionAfter:
		if position.SegmentID != "" {
			index, ok := findSegment(segments, position.SegmentID)
			if !ok {
				return 0, fmt.Errorf("prompt: position segment %q is not found", position.SegmentID)
			}
			return index + 1, nil
		}
		return clamp(*position.Index+1, 0, length), nil
	default:
		return 0, fmt.Errorf("prompt: unsupported compact position %q", position.Kind)
	}
}

func indexSegments(segments []Segment) (map[string]int, error) {
	index := make(map[string]int, len(segments))
	for i, segment := range segments {
		if segment.ID == "" {
			return nil, errors.New("prompt: segment id is required")
		}
		if _, exists := index[segment.ID]; exists {
			return nil, fmt.Errorf("prompt: duplicate segment id %q", segment.ID)
		}
		index[segment.ID] = i
	}
	return index, nil
}

func findSegment(segments []Segment, id string) (int, bool) {
	for i, segment := range segments {
		if segment.ID == id {
			return i, true
		}
	}
	return 0, false
}

func insertSegment(segments []Segment, slot int, segment Segment) []Segment {
	next := make([]Segment, 0, len(segments)+1)
	next = append(next, segments[:slot]...)
	next = append(next, segment)
	next = append(next, segments[slot:]...)
	return next
}

func estimatePrompt(ctx context.Context, estimator token.Estimator, request token.EstimateRequest, budget token.Budget, prompt Prompt) (*token.Estimate, error) {
	if estimator == nil {
		return nil, nil
	}
	if budget.ContextLimit > 0 {
		request.ContextLimit = budget.ContextLimit
	}
	if budget.MaxOutputTokens > 0 {
		request.MaxOutputTokens = budget.MaxOutputTokens
	}
	request.Messages = promptTokenMessages(prompt)
	estimate, err := estimator.CountPromptTokens(ctx, request)
	if err != nil {
		return nil, err
	}
	return &estimate, nil
}

func promptTokenMessages(prompt Prompt) []token.Message {
	messages := make([]token.Message, 0, len(prompt.Segments))
	for _, segment := range prompt.Segments {
		messages = append(messages, token.Message{
			Role:    segment.Role,
			Content: segment.Content,
		})
	}
	return messages
}

func clonePrompt(prompt Prompt) Prompt {
	cloned := Prompt{
		ID:       prompt.ID,
		Segments: make([]Segment, 0, len(prompt.Segments)),
		Metadata: cloneMetadata(prompt.Metadata),
	}
	for _, segment := range prompt.Segments {
		cloned.Segments = append(cloned.Segments, cloneSegment(segment))
	}
	return cloned
}

func cloneOps(ops []CompactOp) []CompactOp {
	if len(ops) == 0 {
		return nil
	}
	cloned := make([]CompactOp, len(ops))
	for i, op := range ops {
		cloned[i] = op
		if op.Replacement != nil {
			cloned[i].Replacement = cloneSegmentPtr(*op.Replacement)
		}
		if op.Position.Index != nil {
			index := *op.Position.Index
			cloned[i].Position.Index = &index
		}
	}
	return cloned
}

func cloneSegmentPtr(segment Segment) *Segment {
	cloned := cloneSegment(segment)
	return &cloned
}

func intPtr(value int) *int {
	return &value
}

func clamp(value int, min int, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
