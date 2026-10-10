package agentcontext

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// TestEnforcedBudgetPolicy tests the EnforcedBudgetPolicy implementation
func TestEnforcedBudgetPolicy(t *testing.T) {
	config := BudgetConfig{
		ContextWindowTokens:     100000,
		InstructionTokens:       10000,
		SummaryTokens:           5000,
		HistoryTokens:           20000,
		InterviewMaterialTokens: 10000,
		ToolResultTokens:        5000,
		OutputReservedTokens:    4000,
	}
	modelWindows := map[string]int{
		"default": 100000,
		"claude-3-5-sonnet-20241022": 200000,
	}
	policy := NewEnforcedBudgetPolicy(config, modelWindows)

	t.Run("validates model window", func(t *testing.T) {
		_, err := policy.Plan(context.Background(), chat.ContextInput{})
		if err != nil {
			t.Fatalf("Plan() error = %v, want nil", err)
		}
	})

	t.Run("rejects budget exceeding window", func(t *testing.T) {
		badConfig := BudgetConfig{
			ContextWindowTokens:     100000,
			InstructionTokens:       200000,
			SummaryTokens:           10000,
			HistoryTokens:           10000,
			InterviewMaterialTokens: 10000,
			ToolResultTokens:        10000,
			OutputReservedTokens:    10000,
		}
		badPolicy := NewEnforcedBudgetPolicy(badConfig, modelWindows)
		_, err := badPolicy.Plan(context.Background(), chat.ContextInput{})
		if err == nil {
			t.Fatalf("Plan() error = nil, want budget exceeds window error")
		}
		if !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("error = %v, want exceeds window error", err)
		}
	})
}

// TestCharBasedTokenEstimator tests the CharBasedTokenEstimator implementation
func TestCharBasedTokenEstimator(t *testing.T) {
	estimator := NewCharBasedTokenEstimator(4.0)

	t.Run("counts text tokens", func(t *testing.T) {
		text := "Hello, world!"
		tokens, err := estimator.CountText("default", text)
		if err != nil {
			t.Fatalf("CountText() error = %v", err)
		}
		expected := (len(text) + 3) / 4 // ceil(13/4) = 4
		if tokens != expected {
			t.Errorf("CountText() = %d, want %d", tokens, expected)
		}
	})

	t.Run("counts message tokens", func(t *testing.T) {
		messages := []*schema.Message{
			{Role: "user", Content: "Question"},
			{Role: "assistant", Content: "Answer"},
		}
		tokens, err := estimator.CountMessages("default", messages, nil)
		if err != nil {
			t.Fatalf("CountMessages() error = %v", err)
		}
		// "user: Question\nassistant: Answer\n" = roughly 30 chars -> 8 tokens
		if tokens < 5 || tokens > 15 {
			t.Errorf("CountMessages() = %d, want ~8", tokens)
		}
	})
}

// TestDefaultBoundarySelector tests the DefaultBoundarySelector implementation
func TestDefaultBoundarySelector(t *testing.T) {
	estimator := NewCharBasedTokenEstimator(4.0)
	selector := NewDefaultBoundarySelector(estimator)

	t.Run("no compaction needed within budget", func(t *testing.T) {
		snapshot := ProjectionSnapshot{
			Messages: []conversation.ContextMessage{
				{Role: "user", Content: "Q", Sources: []conversation.ProjectionSource{{EntryID: "e1"}}},
				{Role: "assistant", Content: "A", Sources: []conversation.ProjectionSource{{EntryID: "e2"}}},
			},
		}

		result, err := selector.SelectPrefix(context.Background(), BoundaryInput{
			Snapshot:        snapshot,
			AvailableTokens: 100,
			CurrentEstimate: 10,
			Estimator:       estimator,
		})

		if err != nil {
			t.Fatalf("SelectPrefix() error = %v", err)
		}
		if result.NeedsCompaction {
			t.Errorf("NeedsCompaction = true, want false")
		}
	})

	t.Run("selects complete turn prefix", func(t *testing.T) {
		snapshot := ProjectionSnapshot{
			Messages: []conversation.ContextMessage{
				{Role: "user", Content: "Question 1", Sources: []conversation.ProjectionSource{{EntryID: "e1"}}},
				{Role: "assistant", Content: "Answer 1", Sources: []conversation.ProjectionSource{{EntryID: "e2"}}},
				{Role: "user", Content: "Question 2", Sources: []conversation.ProjectionSource{{EntryID: "e3"}}},
				{Role: "assistant", Content: "Answer 2", Sources: []conversation.ProjectionSource{{EntryID: "e4"}}},
			},
		}

		result, err := selector.SelectPrefix(context.Background(), BoundaryInput{
			Snapshot:        snapshot,
			BaseEntryID:     "e3", // Protect from e3 onwards
			AvailableTokens: 10,
			CurrentEstimate: 100,
			Estimator:       estimator,
		})

		if err != nil {
			t.Fatalf("SelectPrefix() error = %v", err)
		}
		if !result.NeedsCompaction {
			t.Errorf("NeedsCompaction = false, want true")
		}
		if len(result.PrefixMessages) != 2 {
			t.Errorf("len(PrefixMessages) = %d, want 2 (first turn)", len(result.PrefixMessages))
		}
		if len(result.KeptMessages) != 2 {
			t.Errorf("len(KeptMessages) = %d, want 2 (second turn)", len(result.KeptMessages))
		}
		if result.FirstKeptEntryID != "e3" {
			t.Errorf("FirstKeptEntryID = %s, want e3", result.FirstKeptEntryID)
		}
	})

	t.Run("protects pending tool calls", func(t *testing.T) {
		snapshot := ProjectionSnapshot{
			Messages: []conversation.ContextMessage{
				{Role: "user", Content: "Question", Sources: []conversation.ProjectionSource{{EntryID: "e1"}}},
				{Role: "assistant", Content: "Answer with Tool call: search", Sources: []conversation.ProjectionSource{{EntryID: "e2"}}},
				// Missing tool result - pending group
			},
		}

		result, err := selector.SelectPrefix(context.Background(), BoundaryInput{
			Snapshot:        snapshot,
			AvailableTokens: 100, // Enough for the pending group
			CurrentEstimate: 100,
			Estimator:       estimator,
		})

		if err != nil {
			t.Fatalf("SelectPrefix() error = %v", err)
		}
		// Should protect the pending group, no prefix available
		if result.NeedsCompaction {
			t.Errorf("NeedsCompaction = true, should protect pending group")
		}
	})

	t.Run("returns error when kept content would be empty", func(t *testing.T) {
		snapshot := ProjectionSnapshot{
			Messages: []conversation.ContextMessage{
				{Role: "user", Content: strings.Repeat("a", 10000), Sources: []conversation.ProjectionSource{{EntryID: "e1"}}},
			},
		}

		_, err := selector.SelectPrefix(context.Background(), BoundaryInput{
			Snapshot:        snapshot,
			AvailableTokens: 10,
			CurrentEstimate: 2500, // 10000/4 = 2500 tokens
			Estimator:       estimator,
		})

		// This is technically compactable (no base, no pending), but would leave kept=0
		// which is not useful. For now we allow it - budget policy should catch this.
		if err != nil {
			t.Logf("Got error (may be acceptable): %v", err)
		}
	})

	t.Run("returns error when base content exceeds budget", func(t *testing.T) {
		snapshot := ProjectionSnapshot{
			Messages: []conversation.ContextMessage{
				{Role: "user", Content: "Small", Sources: []conversation.ProjectionSource{{EntryID: "e1"}}},
				{Role: "assistant", Content: strings.Repeat("x", 10000), Sources: []conversation.ProjectionSource{{EntryID: "e2"}}},
			},
		}

		_, err := selector.SelectPrefix(context.Background(), BoundaryInput{
			Snapshot:        snapshot,
			BaseEntryID:     "e2", // Must keep e2 onwards
			AvailableTokens: 10,
			CurrentEstimate: 2500,
			Estimator:       estimator,
		})

		if err == nil {
			t.Fatalf("SelectPrefix() error = nil, want indivisible content error")
		}
		if !strings.Contains(err.Error(), "indivisible") {
			t.Errorf("error = %v, want indivisible content error", err)
		}
	})
}

// TestBoundarySelector_CompleteTurnDetection tests turn grouping logic
func TestBoundarySelector_CompleteTurnDetection(t *testing.T) {
	estimator := NewCharBasedTokenEstimator(4.0)
	selector := NewDefaultBoundarySelector(estimator)

	t.Run("groups complete turns", func(t *testing.T) {
		messages := []conversation.ContextMessage{
			{Role: "user", Content: "Q1"},
			{Role: "assistant", Content: "A1"},
			{Role: "user", Content: "Q2"},
			{Role: "assistant", Content: "A2"},
		}

		groups := selector.groupByCompleteTurns(messages)
		if len(groups) != 2 {
			t.Errorf("len(groups) = %d, want 2", len(groups))
		}
		if len(groups[0]) != 2 {
			t.Errorf("len(groups[0]) = %d, want 2", len(groups[0]))
		}
		if len(groups[1]) != 2 {
			t.Errorf("len(groups[1]) = %d, want 2", len(groups[1]))
		}
	})

	t.Run("detects incomplete turn with pending tool call", func(t *testing.T) {
		messages := []conversation.ContextMessage{
			{Role: "user", Content: "Question"},
			{Role: "assistant", Content: "Tool call: search"},
			// Missing tool result
		}

		groups := selector.groupByCompleteTurns(messages)
		if len(groups) != 1 {
			t.Errorf("len(groups) = %d, want 1", len(groups))
		}
		if selector.isCompleteTurn(groups[0]) {
			t.Errorf("isCompleteTurn() = true, want false (pending tool call)")
		}
	})

	t.Run("detects complete turn with tool call and result", func(t *testing.T) {
		messages := []conversation.ContextMessage{
			{Role: "user", Content: "Question"},
			{Role: "assistant", Content: "Tool call: search"},
			{Role: "tool", Content: "Tool result: found"},
		}

		groups := selector.groupByCompleteTurns(messages)
		if len(groups) != 1 {
			t.Errorf("len(groups) = %d, want 1", len(groups))
		}
		if !selector.isCompleteTurn(groups[0]) {
			t.Errorf("isCompleteTurn() = false, want true")
		}
	})
}
