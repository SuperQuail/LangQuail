package prompt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/superquail/langquail/prompt"
	"github.com/superquail/langquail/token"
)

func TestCompactAppliesKeepDropReplaceAddAndDefaultKeep(t *testing.T) {
	result, err := prompt.Compact(context.Background(), prompt.CompactRequest{
		Prompt: compactFixture("a", "b", "c", "d"),
		Plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
			prompt.KeepSegment("a"),
			prompt.DropSegment("b"),
			prompt.ReplaceSegment("c", prompt.Segment{ID: "c", Role: prompt.RoleAssistant, Content: "replacement"}),
			prompt.AddSegment(prompt.Segment{ID: "x", Role: prompt.RoleUser, Content: "added"}, prompt.PositionAfterSegment("a")),
		}},
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	assertSegmentContents(t, result.Prompt, []string{"a", "added", "replacement", "d"})
	if !result.Changed {
		t.Fatalf("Changed = false")
	}
	if len(result.Ops) != 4 {
		t.Fatalf("Ops = %#v", result.Ops)
	}
	if result.Prompt.Segments[3].ID != "d" {
		t.Fatalf("unlisted segment was not kept: %#v", result.Prompt.Segments)
	}
}

func TestCompactAddPositions(t *testing.T) {
	tests := []struct {
		name     string
		position prompt.CompactPosition
		want     []string
	}{
		{name: "start", position: prompt.PositionStart(), want: []string{"x", "a", "b"}},
		{name: "end", position: prompt.PositionEnd(), want: []string{"a", "b", "x"}},
		{name: "index zero", position: prompt.PositionIndex(0), want: []string{"x", "a", "b"}},
		{name: "index len", position: prompt.PositionIndex(2), want: []string{"a", "b", "x"}},
		{name: "before segment", position: prompt.PositionBeforeSegment("b"), want: []string{"a", "x", "b"}},
		{name: "after segment", position: prompt.PositionAfterSegment("a"), want: []string{"a", "x", "b"}},
		{name: "before index", position: prompt.PositionBeforeIndex(1), want: []string{"a", "x", "b"}},
		{name: "after index", position: prompt.PositionAfterIndex(0), want: []string{"a", "x", "b"}},
		{name: "before low index clamps to start", position: prompt.PositionBeforeIndex(-10), want: []string{"x", "a", "b"}},
		{name: "before high index clamps to end", position: prompt.PositionBeforeIndex(99), want: []string{"a", "b", "x"}},
		{name: "after low index clamps to start", position: prompt.PositionAfterIndex(-10), want: []string{"x", "a", "b"}},
		{name: "after high index clamps to end", position: prompt.PositionAfterIndex(99), want: []string{"a", "b", "x"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := prompt.Compact(context.Background(), prompt.CompactRequest{
				Prompt: compactFixture("a", "b"),
				Plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
					prompt.AddSegment(prompt.Segment{ID: "x", Role: prompt.RoleUser, Content: "x"}, tt.position),
				}},
			})
			if err != nil {
				t.Fatalf("Compact() error = %v", err)
			}
			assertSegmentContents(t, result.Prompt, tt.want)
		})
	}
}

func TestCompactDropsTwoSegmentsAndAddsSummaryAtIndex(t *testing.T) {
	result, err := prompt.Compact(context.Background(), prompt.CompactRequest{
		Prompt: compactFixture("intro", "detail-a", "detail-b", "tail"),
		Plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
			prompt.DropSegment("detail-a"),
			prompt.DropSegment("detail-b"),
			prompt.AddSegment(prompt.Segment{ID: "summary", Role: prompt.RoleUser, Content: "summary"}, prompt.PositionIndex(1)),
		}},
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	assertSegmentContents(t, result.Prompt, []string{"intro", "summary", "tail"})
}

func TestCompactErrors(t *testing.T) {
	index := 0
	tests := []struct {
		name string
		plan prompt.CompactPlan
		want string
	}{
		{
			name: "duplicate mutation",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
				prompt.DropSegment("a"),
				prompt.ReplaceSegment("a", prompt.Segment{ID: "a", Content: "next"}),
			}},
			want: "modified more than once",
		},
		{
			name: "unknown segment",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
				prompt.DropSegment("missing"),
			}},
			want: "not found",
		},
		{
			name: "missing replace replacement",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{{
				Action:    prompt.CompactReplace,
				SegmentID: "a",
			}}},
			want: "requires replacement",
		},
		{
			name: "missing add replacement",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{{
				Action:   prompt.CompactAdd,
				Position: prompt.PositionEnd(),
			}}},
			want: "requires replacement",
		},
		{
			name: "missing add position",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{{
				Action:      prompt.CompactAdd,
				Replacement: &prompt.Segment{ID: "x", Content: "x"},
			}}},
			want: "unsupported compact position",
		},
		{
			name: "index out of range",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
				prompt.AddSegment(prompt.Segment{ID: "x", Content: "x"}, prompt.PositionIndex(3)),
			}},
			want: "out of range",
		},
		{
			name: "index with segment id",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{{
				Action:      prompt.CompactAdd,
				Replacement: &prompt.Segment{ID: "x", Content: "x"},
				Position:    prompt.CompactPosition{Kind: prompt.CompactPositionIndex, SegmentID: "a", Index: &index},
			}}},
			want: "does not accept segment id",
		},
		{
			name: "before with segment and index",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{{
				Action:      prompt.CompactAdd,
				Replacement: &prompt.Segment{ID: "x", Content: "x"},
				Position:    prompt.CompactPosition{Kind: prompt.CompactPositionBefore, SegmentID: "a", Index: &index},
			}}},
			want: "exactly one",
		},
		{
			name: "unknown position segment",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
				prompt.AddSegment(prompt.Segment{ID: "x", Content: "x"}, prompt.PositionBeforeSegment("missing")),
			}},
			want: "position segment",
		},
		{
			name: "duplicate final segment",
			plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
				prompt.AddSegment(prompt.Segment{ID: "a", Content: "x"}, prompt.PositionEnd()),
			}},
			want: "duplicate segment id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prompt.Compact(context.Background(), prompt.CompactRequest{
				Prompt: compactFixture("a", "b"),
				Plan:   tt.plan,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Compact() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCompactEstimatesBeforeAndAfter(t *testing.T) {
	estimator := &compactEstimator{}
	result, err := prompt.Compact(context.Background(), prompt.CompactRequest{
		Prompt: compactFixture("a", "b"),
		Plan: prompt.CompactPlan{Ops: []prompt.CompactOp{
			prompt.DropSegment("b"),
		}},
		Estimator: estimator,
		Budget: token.Budget{
			ContextLimit:    100,
			MaxOutputTokens: 10,
		},
		EstimateRequest: token.EstimateRequest{Model: "fake-model"},
	})
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if result.BeforeEstimate == nil || result.BeforeEstimate.InputTokens != 2 {
		t.Fatalf("BeforeEstimate = %#v", result.BeforeEstimate)
	}
	if result.AfterEstimate == nil || result.AfterEstimate.InputTokens != 1 {
		t.Fatalf("AfterEstimate = %#v", result.AfterEstimate)
	}
	if len(estimator.requests) != 2 {
		t.Fatalf("requests = %#v", estimator.requests)
	}
	if estimator.requests[0].ContextLimit != 100 || estimator.requests[0].MaxOutputTokens != 10 {
		t.Fatalf("budget was not applied: %#v", estimator.requests[0])
	}
}

type compactEstimator struct {
	requests []token.EstimateRequest
}

func (e *compactEstimator) CountPromptTokens(_ context.Context, request token.EstimateRequest) (token.Estimate, error) {
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

func compactFixture(contents ...string) prompt.Prompt {
	segments := make([]prompt.Segment, 0, len(contents))
	for _, content := range contents {
		segments = append(segments, prompt.Segment{
			ID:      content,
			Role:    prompt.RoleUser,
			Source:  "test",
			Content: content,
		})
	}
	return prompt.Prompt{
		ID:       "compact.fixture",
		Segments: segments,
	}
}

func assertSegmentContents(t *testing.T, actual prompt.Prompt, want []string) {
	t.Helper()
	if len(actual.Segments) != len(want) {
		t.Fatalf("segments = %#v, want %v", actual.Segments, want)
	}
	for i, content := range want {
		if actual.Segments[i].Content != content {
			t.Fatalf("segments = %#v, want contents %v", actual.Segments, want)
		}
	}
}
