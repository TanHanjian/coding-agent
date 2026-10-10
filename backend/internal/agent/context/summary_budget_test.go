package agentcontext

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestManagerSummaryBudgetGuard(t *testing.T) {
	const (
		modelID      = "summary-test-model"
		outputLimit  = 128
		safetyMargin = 64
	)
	oldPart1 := strings.Repeat("old history ", 250)
	oldPart2 := strings.Repeat("more history ", 250)
	oldResponse := "Old response"
	recentText := "Recent question"
	wantUserPrompt := "Summarize this conversation prefix:\n\n[user]: " + oldPart1 + oldPart2 + "\n[assistant]: " + oldResponse + "\n"
	requestEstimator := NewRequestTokenEstimator()
	userOnlyTokens, err := requestEstimator.CountMessages(modelID, []*schema.Message{schema.UserMessage(wantUserPrompt)}, nil)
	if err != nil {
		t.Fatalf("count history-only prompt: %v", err)
	}

	for _, tc := range []struct {
		name          string
		window        int
		countErr      error
		negativeCount bool
		wantErr       error
	}{
		{
			// The history-only request fits; the system instructions make the
			// completed summary request exceed the available input capacity.
			name:    "completed_prompt_exceeds_budget",
			window:  userOnlyTokens + 32 + outputLimit + safetyMargin,
			wantErr: chat.ErrContextBudgetExceeded,
		},
		{
			name:     "counting_error",
			window:   10000,
			countErr: errors.New("summary request counting failed"),
			wantErr:  chat.ErrContextCountingFailed,
		},
		{
			name:          "negative_count",
			window:        10000,
			negativeCount: true,
			wantErr:       chat.ErrContextCountingFailed,
		},
		{
			name:   "allowed_summary_is_persisted",
			window: 10000,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e001, e002, e003 := conversation.EntryID("e001"), conversation.EntryID("e002"), conversation.EntryID("e003")
			projectionReader := &mockProjectionReader{snapshot: &conversation.ProjectionSnapshot{
				Head: conversation.EntryHead{ConversationID: "conv", ActiveLeafID: &e003, Version: 1},
				Entries: []conversation.ConversationEntry{
					{
						ID: e001, ConversationID: "conv", Kind: conversation.EntryKindMessage,
						PayloadVersion: conversation.EntryPayloadV1,
						Message: &conversation.EntryMessage{
							Role: conversation.EntryRoleUser,
							Content: []conversation.EntryPart{
								{Type: conversation.EntryPartText, Text: &oldPart1},
								{Type: conversation.EntryPartText, Text: &oldPart2},
							},
						},
					},
					{
						ID: e002, ConversationID: "conv", ParentID: &e001, Kind: conversation.EntryKindMessage,
						PayloadVersion: conversation.EntryPayloadV1, Depth: 1,
						Message: &conversation.EntryMessage{
							Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopNormal,
							Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &oldResponse}},
						},
					},
					{
						ID: e003, ConversationID: "conv", ParentID: &e002, Kind: conversation.EntryKindMessage,
						PayloadVersion: conversation.EntryPayloadV1, Depth: 2,
						Message: &conversation.EntryMessage{
							Role:    conversation.EntryRoleUser,
							Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &recentText}},
						},
					},
				},
			}}
			headBefore := projectionReader.snapshot.Head
			compactionStore := &summaryBudgetCompactionStore{
				mockCompactionStore: &mockCompactionStore{}, // No existing compaction to reuse.
				head:                &projectionReader.snapshot.Head,
			}

			countCalls, modelCalls := 0, 0
			var countedMessages []*schema.Message
			var countedTokens int
			guardEstimator := TokenEstimatorFunc{
				CountTextFunc: requestEstimator.CountText,
				CountMessagesFunc: func(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error) {
					countCalls++
					if model != modelID {
						t.Errorf("count model = %q, want %q", model, modelID)
					}
					if tools != nil {
						t.Errorf("summary tools = %#v, want nil", tools)
					}
					if len(messages) != 2 || messages[0] == nil || messages[1] == nil {
						t.Fatalf("expected complete system/user summary prompt, got %#v", messages)
					}
					if messages[0].Role != schema.System || !strings.Contains(messages[0].Content, "You are a conversation summarizer.") || !strings.Contains(messages[0].Content, "Output valid JSON") {
						t.Errorf("missing summary system instructions: %#v", messages[0])
					}
					if messages[1].Role != schema.User || messages[1].Content != wantUserPrompt {
						t.Error("summary user prompt must contain the full omitted history, both text parts and assistant response, without the kept turn or an existing summary")
					}
					countedMessages = messages
					count, err := requestEstimator.CountMessages(model, messages, tools)
					if err != nil {
						t.Fatalf("count completed summary request: %v", err)
					}
					countedTokens = count
					if tc.countErr != nil {
						return 0, tc.countErr
					}
					if tc.negativeCount {
						return -1, nil
					}
					return count, nil
				},
			}
			guard, err := NewModelCallGuard(ModelCallConfig{
				ModelID: modelID, ContextWindowTokens: tc.window,
				OutputLimitTokens: outputLimit, SafetyMarginTokens: safetyMargin,
			}, guardEstimator)
			if err != nil {
				t.Fatalf("create summary guard: %v", err)
			}
			summarizer := NewEinoContextSummarizer(GuardedModelInvoker(guard,
				func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
					modelCalls++
					if !reflect.DeepEqual(messages, countedMessages) {
						t.Error("model request differs from the counted request")
					}
					return schema.AssistantMessage(`{"goal":"Test goal","confirmedFacts":["fact1"],"decisions":["decision1"],"constraints":["constraint1"],"openQuestions":["question1"]}`, nil), nil
				}))
			// Preparation needs compaction even when the final summary call fits.
			// Keep its estimator separate so counting failures occur at the guard.
			preparationEstimator := NewCharBasedTokenEstimator(4.0)
			manager := NewManager(
				WithProjectionReader(projectionReader),
				WithBudgetPolicy(NewEnforcedBudgetPolicy(BudgetConfig{
					ContextWindowTokens: tc.window, InstructionTokens: 500,
					SummaryTokens: 100, HistoryTokens: 500, OutputReservedTokens: outputLimit,
				}, map[string]int{"default": tc.window})),
				WithTokenEstimator(preparationEstimator),
				WithBoundarySelector(NewDefaultBoundarySelector(preparationEstimator)),
				WithSummaryCoordinator(NewDefaultSummaryCoordinator(summarizer, preparationEstimator)),
				WithCompactionWriter(compactionStore),
				WithCompactionReader(compactionStore),
			)
			prepared, err := manager.Prepare(context.Background(), chat.ContextInput{
				Conversation: conversation.Conversation{ID: "conv"}, RunID: "run001",
				UserMessage: conversation.Message{ID: "msg001", Content: "Current query"},
			})
			if countCalls != 1 {
				t.Fatalf("summary count calls = %d, want 1", countCalls)
			}
			available := tc.window - outputLimit - safetyMargin
			if tc.wantErr == chat.ErrContextBudgetExceeded && (userOnlyTokens > available || countedTokens <= available) {
				t.Fatalf("invalid budget fixture: history-only=%d, completed=%d, available=%d", userOnlyTokens, countedTokens, available)
			}
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Prepare error = %v, want errors.Is(_, %v)", err, tc.wantErr)
				}
				if !strings.HasPrefix(err.Error(), "prepare summary: generate summary: model invocation failed: ") {
					t.Errorf("error did not pass through summary/manager wrapping: %v", err)
				}
				if modelCalls != 0 || len(compactionStore.compactions) != 0 {
					t.Errorf("rejected summary: model calls=%d, compactions=%d, want both 0", modelCalls, len(compactionStore.compactions))
				}
				if !reflect.DeepEqual(projectionReader.snapshot.Head, headBefore) {
					t.Errorf("rejected summary advanced head: before=%+v, after=%+v", headBefore, projectionReader.snapshot.Head)
				}
				return
			}
			if err != nil {
				t.Fatalf("allowed summary Prepare failed: %v", err)
			}
			if countedTokens > available {
				t.Fatalf("allowed fixture exceeds budget: %d > %d", countedTokens, available)
			}
			if modelCalls != 1 || len(compactionStore.compactions) != 1 {
				t.Fatalf("allowed summary: model calls=%d, compactions=%d, want both 1", modelCalls, len(compactionStore.compactions))
			}
			compaction := compactionStore.compactions[0]
			if compaction.ParentID != e002 || compaction.FirstKeptEntryID != e003 || compaction.Summary.Goal != "Test goal" {
				t.Errorf("unexpected persisted compaction: %+v", compaction)
			}
			if compaction.ExpectedLeaf != e003 || compaction.ExpectedVersion != int(headBefore.Version) {
				t.Errorf("unexpected compaction CAS: %+v", compaction)
			}
			if projectionReader.snapshot.Head.ActiveLeafID == nil || *projectionReader.snapshot.Head.ActiveLeafID != "compaction-entry-id" || projectionReader.snapshot.Head.Version != headBefore.Version+1 {
				t.Errorf("successful append did not advance head: %+v", projectionReader.snapshot.Head)
			}
			if !prepared.Metadata.SummaryUsed || !strings.Contains(prepared.ConversationSummary, "Test goal") || len(prepared.ModelHistory) != 1 || prepared.ModelHistory[0].Content != recentText {
				t.Errorf("unexpected prepared summary/history: %+v", prepared)
			}
		})
	}
}

// Make writes observable through both the existing compaction mock and its head.
// The allowed case verifies that a write advances the head; rejection must not.
type summaryBudgetCompactionStore struct {
	*mockCompactionStore
	head *conversation.EntryHead
}

func (s *summaryBudgetCompactionStore) AppendCompaction(ctx context.Context, input conversation.CompactionInput) (conversation.EntryID, error) {
	id, err := s.mockCompactionStore.AppendCompaction(ctx, input)
	if err != nil {
		return "", err
	}
	s.head.ActiveLeafID = &id
	s.head.Version++
	return id, nil
}
