package agentcontext

import (
	"context"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestManagerCompactionIntegration(t *testing.T) {
	// 老的历史消息 - 很长，会被压缩
	longText := strings.Repeat("old history ", 500) // ~6000 字符 = 1500 tokens
	response1 := "Old response"
	
	// 最近的消息 - 短，必须保留
	recentText := "Recent question"

	// 构造 EntryID
	e001 := conversation.EntryID("e001")
	e002 := conversation.EntryID("e002")
	e003 := conversation.EntryID("e003")

	// Mock ProjectionReader
	projectionReader := &mockProjectionReader{
		snapshot: &conversation.ProjectionSnapshot{
			Head: conversation.EntryHead{
				ConversationID: "conv",
				ActiveLeafID:   &e003,
				Version:        1,
			},
			Entries: []conversation.ConversationEntry{
				{
					ID:             e001,
					ConversationID: "conv",
					ParentID:       nil,
					Kind:           conversation.EntryKindMessage,
					PayloadVersion: conversation.EntryPayloadV1,
					Depth:          0,
					Message: &conversation.EntryMessage{
						Role: conversation.EntryRoleUser,
						Content: []conversation.EntryPart{
							{Type: conversation.EntryPartText, Text: &longText},
						},
					},
				},
				{
					ID:             e002,
					ConversationID: "conv",
					ParentID:       &e001,
					Kind:           conversation.EntryKindMessage,
					PayloadVersion: conversation.EntryPayloadV1,
					Depth:          1,
					Message: &conversation.EntryMessage{
						Role:       conversation.EntryRoleAssistant,
						StopReason: conversation.EntryStopNormal,
						Content: []conversation.EntryPart{
							{Type: conversation.EntryPartText, Text: &response1},
						},
					},
				},
				{
					ID:             e003,
					ConversationID: "conv",
					ParentID:       &e002,
					Kind:           conversation.EntryKindMessage,
					PayloadVersion: conversation.EntryPayloadV1,
					Depth:          2,
					Message: &conversation.EntryMessage{
						Role: conversation.EntryRoleUser,
						Content: []conversation.EntryPart{
							{Type: conversation.EntryPartText, Text: &recentText},
						},
					},
				},
			},
		},
	}

	// Mock CompactionStore
	compactionStore := &mockCompactionStore{
		compactions: []conversation.CompactionInput{},
	}

	// Mock Summarizer
	summarizer := &mockSummarizer{
		summary: ContextSummary{
			Goal:           "Test goal",
			ConfirmedFacts: []string{"fact1"},
			Decisions:      []string{"decision1"},
			Constraints:    []string{"constraint1"},
			OpenQuestions:  []string{"question1"},
		},
	}

	// 组装 Manager
	budgetConfig := BudgetConfig{
		ContextWindowTokens:     10000,
		InstructionTokens:       500,
		SummaryTokens:           100,
		HistoryTokens:           500, // 限制历史只有500 tokens
		InterviewMaterialTokens: 0,
		ToolResultTokens:        0,
		OutputReservedTokens:    200,
	}

	modelWindows := map[string]int{
		"default":                    200000,
		"claude-3-5-sonnet-20241022": 200000,
	}

	estimator := NewCharBasedTokenEstimator(4.0)

	manager := NewManager(
		WithProjectionReader(projectionReader),
		WithBudgetPolicy(NewEnforcedBudgetPolicy(budgetConfig, modelWindows)),
		WithTokenEstimator(estimator),
		WithBoundarySelector(NewDefaultBoundarySelector(estimator)),
		WithSummaryCoordinator(NewDefaultSummaryCoordinator(summarizer, estimator)),
		WithCompactionWriter(compactionStore),
		WithCompactionReader(compactionStore),
	)

	// 构造输入
	input := chat.ContextInput{
		Conversation: conversation.Conversation{ID: "conv"},
		RunID:        "run001",
		UserMessage: conversation.Message{
			ID:      "msg001",
			Content: "Current query",
		},
	}

	// 第一次 Prepare：应该触发压缩
	prepared1, err := manager.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("first Prepare failed: %v", err)
	}

	// 验证：应该有摘要
	if prepared1.ConversationSummary == "" {
		t.Error("expected ConversationSummary, got empty")
	}

	// 验证：应该保留了最近的消息
	if len(prepared1.ModelHistory) == 0 {
		t.Error("expected ModelHistory to contain kept messages")
	}

	// 验证：应该写入了一个 compaction
	if len(compactionStore.compactions) != 1 {
		t.Fatalf("expected 1 compaction, got %d", len(compactionStore.compactions))
	}

	compaction := compactionStore.compactions[0]
	if compaction.FirstKeptEntryID != e003 {
		t.Errorf("expected FirstKeptEntryID=e003, got %s", compaction.FirstKeptEntryID)
	}
	if compaction.Summary.Goal != "Test goal" {
		t.Errorf("expected Summary.Goal='Test goal', got %s", compaction.Summary.Goal)
	}

	// 第二次 Prepare：应该复用 compaction
	compactionStore.shouldReturnCompaction = true
	prepared2, err := manager.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("second Prepare failed: %v", err)
	}

	// 验证：应该有相同的摘要
	if prepared2.ConversationSummary != prepared1.ConversationSummary {
		t.Error("expected same ConversationSummary on reuse")
	}

	// 验证：没有写入新的 compaction
	if len(compactionStore.compactions) != 1 {
		t.Errorf("expected 1 compaction (reused), got %d", len(compactionStore.compactions))
	}
}

// mockProjectionReader
type mockProjectionReader struct {
	snapshot *conversation.ProjectionSnapshot
	err      error
}

func (m *mockProjectionReader) ReadProjection(ctx context.Context, query conversation.ProjectionQuery) (conversation.ProjectionSnapshot, error) {
	if m.err != nil {
		return conversation.ProjectionSnapshot{}, m.err
	}
	return *m.snapshot, nil
}

// mockCompactionStore implements both CompactionWriter and CompactionReader
type mockCompactionStore struct {
	compactions             []conversation.CompactionInput
	shouldReturnCompaction  bool
	writeErr                error
	readErr                 error
}

func (m *mockCompactionStore) AppendCompaction(ctx context.Context, input conversation.CompactionInput) (conversation.EntryID, error) {
	if m.writeErr != nil {
		return "", m.writeErr
	}
	m.compactions = append(m.compactions, input)
	return conversation.EntryID("compaction-entry-id"), nil
}

func (m *mockCompactionStore) ReadLatestCompaction(ctx context.Context, query conversation.CompactionQuery) (*conversation.CompactionEntry, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	// 第一次调用返回空（没有现有压缩）
	if !m.shouldReturnCompaction {
		return nil, nil
	}
	// 第二次调用返回第一次写入的压缩
	if len(m.compactions) == 0 {
		return nil, nil
	}
	comp := m.compactions[0]
	return &conversation.CompactionEntry{
		FirstKeptEntryID: comp.FirstKeptEntryID,
		TokensBefore:     comp.TokensBefore,
		Summary:          comp.Summary,
	}, nil
}

// mockSummarizer
type mockSummarizer struct {
	summary ContextSummary
	err     error
}

func (m *mockSummarizer) Summarize(ctx context.Context, prompt SummaryPrompt) (ContextSummary, error) {
	if m.err != nil {
		return ContextSummary{}, m.err
	}
	return m.summary, nil
}
