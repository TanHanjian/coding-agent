package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func projectionQuery(turn chat.BeginTurnResult) conversation.ProjectionQuery {
	query := conversation.ProjectionQuery{ConversationID: turn.Conversation.ID, UserMessageID: turn.UserMessage.ID}
	if turn.Run != nil {
		query.RunID = string(turn.Run.ID)
	}
	return query
}

func TestProjectionSnapshotSurvivesConcurrentTerminalCommit(t *testing.T) {
	db, ctx, reader := runFixture(t)
	turn := lifecycleBegin(t, ctx, reader, true)
	var path string
	if err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	writerDB, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writerDB.Close() })
	writer, err := NewAgentRunRepository(writerDB)
	if err != nil {
		t.Fatal(err)
	}
	var committed bool
	var writeErr error
	callback := db.ORM(ctx).Callback().Query()
	const name = "projection_test:concurrent_finish"
	if err := callback.After("gorm:query").Register(name, func(tx *gorm.DB) {
		if committed || tx.Statement.Table != "conversation_entry_heads" || tx.Error != nil {
			return
		}
		committed = true
		_, writeErr = writer.FinishAssistant(ctx, lifecycleFinish(t, *turn.Run, conversation.MessageStatusCompleted))
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = callback.Remove(name) })
	snapshot, err := reader.ReadProjection(ctx, projectionQuery(turn))
	if !committed || writeErr != nil {
		t.Fatalf("concurrent write committed=%v err=%v", committed, writeErr)
	}
	if err != nil || len(snapshot.Entries) != 0 || snapshot.Head.Version != turn.Run.HeadVersion {
		t.Fatalf("mixed snapshot = %+v, err=%v", snapshot, err)
	}
	// A new snapshot sees the terminal Run and rejects preparing it again.
	if _, err := reader.ReadProjection(ctx, projectionQuery(turn)); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("terminal read = %v", err)
	}
}

func TestProjectionSnapshotMixedHistoryAndCurrentUser(t *testing.T) {
	_, ctx, r := runFixture(t)
	old, err := r.turns.BeginTurn(ctx, runInput("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.turns.AppendAssistantText(ctx, old.AssistantMessage.ID, "legacy answer"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.turns.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: old.AssistantMessage.ID, Status: conversation.MessageStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	turn, err := r.BeginTurn(ctx, runInput("tree-first"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ReadProjection(ctx, projectionQuery(turn))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LegacyBeforeSequence != turn.UserMessage.Sequence || len(snapshot.Legacy) != 2 || len(snapshot.Entries) != 0 {
		t.Fatalf("first snapshot = %+v", snapshot)
	}
	started, err := r.MarkRunning(ctx, turn.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, started, conversation.MessageStatusCompleted)); err != nil {
		t.Fatal(err)
	}
	next, err := r.BeginTurn(ctx, runInput("tree-second"))
	if err != nil {
		t.Fatal(err)
	}
	before := lifecycleSnapshot(t, ctx, r.db)
	manager := agentcontext.NewManager(agentcontext.WithProjectionReader(r))
	prepared, err := manager.Prepare(ctx, chat.ContextInput{Conversation: next.Conversation, RunID: next.Run.ID, UserMessage: next.UserMessage, History: next.History})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.ModelHistory) != 4 || prepared.History != nil || prepared.Query != next.UserMessage.Content || !prepared.Metadata.ProjectionUsed || prepared.Metadata.LegacyBeforeSequence != turn.UserMessage.Sequence {
		t.Fatalf("prepared = %+v", prepared)
	}
	if prepared.ModelHistory[0].Sources[0].MessageID != old.UserMessage.ID || prepared.ModelHistory[2].Sources[0].EntryID != turn.Run.UserEntryID {
		t.Fatalf("sources = %+v", prepared.ModelHistory)
	}
	for _, message := range prepared.ModelHistory {
		for _, source := range message.Sources {
			if source.EntryID == next.Run.UserEntryID || source.MessageID == next.UserMessage.ID {
				t.Fatal("current user repeated in history")
			}
		}
	}
	if after := lifecycleSnapshot(t, ctx, r.db); after != before {
		t.Fatal("projection wrote persisted state")
	}
}

func TestProjectionSnapshotToolLoopAndFailedBranch(t *testing.T) {
	_, ctx, r := runFixture(t)
	turn := lifecycleBegin(t, ctx, r, true)
	calls := lifecycleCommitCall(t, ctx, r, *turn.Run)
	results, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: turn.Run.ID, ExpectedHead: lifecycleToken(t, calls.Run), Messages: lifecycleResults(t, calls.Entries[0].ID)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, results.Run, conversation.MessageStatusCompleted)); err != nil {
		t.Fatal(err)
	}
	failed, err := r.BeginTurn(ctx, runInput("failed"))
	if err != nil {
		t.Fatal(err)
	}
	failedRun, err := r.MarkRunning(ctx, failed.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	failedCall := lifecycleCall(t)
	// Calls are scoped to a path, so create a new identity for this turn.
	policy, _ := conversation.GetToolPolicy("lifecycle-search", "v1")
	call, err := conversation.ProjectSafeToolCall(policy, "failed-call", []byte(`{"q":"failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	failedCall.Content = []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}}
	pending, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: failedRun.ID, ExpectedHead: lifecycleToken(t, failedRun), Messages: []conversation.EntryMessage{failedCall}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadProjection(ctx, projectionQuery(failed)); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("pending read = %v", err)
	}
	if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, pending.Run, conversation.MessageStatusFailed)); err != nil {
		t.Fatal(err)
	}
	next, err := r.BeginTurn(ctx, runInput("after-failed"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.ReadProjection(ctx, projectionQuery(next))
	if err != nil {
		t.Fatal(err)
	}
	history, err := agentcontext.ProjectSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("history = %+v", history)
	}
	group := history[1]
	if group.DowngradePolicy != conversation.ToolHistoryPolicyVersion || len(group.Sources) != 4 || !strings.Contains(group.Content, `"isError":true`) {
		t.Fatalf("group = %+v", group)
	}
	for _, message := range history {
		if strings.Contains(message.Content, "failed-call") || strings.Contains(message.Content, "must disappear") {
			t.Fatal("failed or unapproved facts in history")
		}
	}
	modelHistory, err := chat.ContextMessagesToSchema(history)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range modelHistory {
		if len(message.ToolCalls) != 0 || message.ToolCallID != "" || message.Extra["context_sources"] == nil {
			t.Fatalf("model protocol/source = %+v", message)
		}
	}
}

func TestProjectionFirstTreeFailureKeepsBoundaryAndClosedFacts(t *testing.T) {
	for _, status := range []conversation.MessageStatus{conversation.MessageStatusFailed, conversation.MessageStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			_, ctx, r := runFixture(t)
			old, err := r.turns.BeginTurn(ctx, runInput("legacy-prefix"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.turns.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: old.AssistantMessage.ID, Status: conversation.MessageStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			turn := lifecycleBegin(t, ctx, r, true)
			calls := lifecycleCommitCall(t, ctx, r, *turn.Run)
			closed, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: turn.Run.ID, ExpectedHead: lifecycleToken(t, calls.Run), Messages: lifecycleResults(t, calls.Entries[0].ID)})
			if err != nil {
				t.Fatal(err)
			}
			policy, _ := conversation.GetToolPolicy("lifecycle-search", "v1")
			call, err := conversation.ProjectSafeToolCall(policy, "abandoned-call", []byte(`{"q":"not in history"}`))
			if err != nil {
				t.Fatal(err)
			}
			pendingMessage := conversation.EntryMessage{Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools, Content: []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}}}
			pending, err := r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{RunID: turn.Run.ID, ExpectedHead: lifecycleToken(t, closed.Run), Messages: []conversation.EntryMessage{pendingMessage}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, pending.Run, status)); err != nil {
				t.Fatal(err)
			}
			next, err := r.BeginTurn(ctx, runInput("after-first-failure"))
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := r.ReadProjection(ctx, projectionQuery(next))
			if err != nil {
				t.Fatal(err)
			}
			history, err := agentcontext.ProjectSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.LegacyBeforeSequence != turn.UserMessage.Sequence || len(history) != 4 || history[0].Sources[0].MessageID != old.UserMessage.ID || history[3].DowngradePolicy != conversation.ToolHistoryPolicyVersion {
				t.Fatalf("history=%+v boundary=%d", history, snapshot.LegacyBeforeSequence)
			}
			for _, message := range history {
				if strings.Contains(message.Content, "abandoned-call") {
					t.Fatal("abandoned facts survived")
				}
			}
		})
	}
}

func TestProjectionSnapshotRejectsAmbiguousCompatibility(t *testing.T) {
	t.Run("tree without run binding", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		turn := lifecycleBegin(t, ctx, r, false)
		query := projectionQuery(turn)
		query.RunID = ""
		if _, err := r.ReadProjection(ctx, query); !errors.Is(err, conversation.ErrEntryCorrupt) {
			t.Fatalf("unbound tree = %v", err)
		}
	})
	t.Run("uncovered legacy after watermark", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		turn := lifecycleBegin(t, ctx, r, true)
		if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, *turn.Run, conversation.MessageStatusCompleted)); err != nil {
			t.Fatal(err)
		}
		old, err := r.turns.BeginTurn(ctx, runInput("uncovered-legacy"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.turns.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: old.AssistantMessage.ID, Status: conversation.MessageStatusCompleted}); err != nil {
			t.Fatal(err)
		}
		next, err := r.BeginTurn(ctx, runInput("next-tree"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.ReadProjection(ctx, projectionQuery(next)); !errors.Is(err, conversation.ErrEntryCorrupt) {
			t.Fatalf("uncovered legacy = %v", err)
		}
	})
	t.Run("wrong user binding", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		turn := lifecycleBegin(t, ctx, r, true)
		if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, *turn.Run, conversation.MessageStatusCompleted)); err != nil {
			t.Fatal(err)
		}
		next, err := r.BeginTurn(ctx, runInput("next-tree"))
		if err != nil {
			t.Fatal(err)
		}
		query := projectionQuery(next)
		query.UserMessageID = turn.UserMessage.ID
		if _, err := r.ReadProjection(ctx, query); !errors.Is(err, conversation.ErrEntryCorrupt) {
			t.Fatalf("wrong user = %v", err)
		}
	})
}

func TestProjectionSnapshotLegacyAndCorruption(t *testing.T) {
	t.Run("pure legacy", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		old, err := r.turns.BeginTurn(ctx, runInput("legacy-only"))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := r.ReadProjection(ctx, projectionQuery(old))
		if err != nil || len(snapshot.Legacy) != 0 || snapshot.LegacyBeforeSequence != old.UserMessage.Sequence {
			t.Fatalf("snapshot = %+v %v", snapshot, err)
		}
	})
	for _, test := range []struct {
		name, sql string
		want      error
	}{
		{"head conflict", `UPDATE conversation_entry_heads SET version = version + 1`, domainerr.ErrConflict},
		{"unknown payload", `UPDATE conversation_entry_nodes SET payload_version = 99`, conversation.ErrEntryUnsupportedVersion},
		{"missing root parent", `UPDATE conversation_entry_nodes SET depth = 1`, conversation.ErrEntryCorrupt},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			turn := lifecycleBegin(t, ctx, r, false)
			if err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				if test.want != domainerr.ErrConflict {
					// Deliberately bypass storage guards only in this isolated corrupt fixture.
					if err := tx.Exec("DROP TRIGGER entry_nodes_no_update").Error; err != nil {
						return err
					}
					if err := tx.Exec("PRAGMA ignore_check_constraints = ON").Error; err != nil {
						return err
					}
					defer tx.Exec("PRAGMA ignore_check_constraints = OFF")
				}
				return tx.Exec(test.sql).Error
			}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := r.ReadProjection(ctx, projectionQuery(turn))
			if !errors.Is(err, test.want) || snapshot.Head.ConversationID != "" {
				t.Fatalf("snapshot = %+v %v", snapshot, err)
			}
		})
	}
	t.Run("no legacy fallback for a tree", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		turn := lifecycleBegin(t, ctx, r, false)
		query := projectionQuery(turn)
		query.RunID = ""
		if _, err := r.ReadProjection(ctx, query); !errors.Is(err, conversation.ErrEntryCorrupt) {
			t.Fatalf("read = %v", err)
		}
	})
	t.Run("capacity", func(t *testing.T) {
		_, ctx, r := runFixture(t)
		turn := lifecycleBegin(t, ctx, r, true)
		if _, err := r.FinishAssistant(ctx, lifecycleFinish(t, *turn.Run, conversation.MessageStatusCompleted)); err != nil {
			t.Fatal(err)
		}
		next, err := r.BeginTurn(ctx, runInput("next"))
		if err != nil {
			t.Fatal(err)
		}
		r.tree.maxPathEntries = 1
		if _, err := r.ReadProjection(ctx, projectionQuery(next)); !errors.Is(err, conversation.ErrEntryCapacity) {
			t.Fatalf("read = %v", err)
		}
	})
}
