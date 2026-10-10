package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func runFixture(t *testing.T) (*storage.DB, context.Context, *AgentRunRepository) {
	t.Helper()
	db, ctx, _ := newEntryTxFixture(t)
	r, err := NewAgentRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, ctx, r
}

func runInput(client string) chat.BeginTurnInput {
	return chat.BeginTurnInput{ConversationID: "entry-tx", ClientMessageID: client, Text: "hello user"}
}

func assertRunCounts(t *testing.T, ctx context.Context, db *storage.DB, messages, entries, heads, runs int) {
	t.Helper()
	for _, want := range []struct {
		table string
		count int
	}{{"messages", messages}, {"conversation_entry_nodes", entries}, {"conversation_entry_heads", heads}, {"agent_runs", runs}} {
		var got int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+want.table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want.count {
			t.Fatalf("%s count = %d, want %d", want.table, got, want.count)
		}
	}
}

func failPendingFixture(t *testing.T, ctx context.Context, db *storage.DB, result chat.BeginTurnResult) {
	t.Helper()
	err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE agent_runs SET status = 'failed', error_code = 'test_failed', finished_at = ? WHERE id = ?`, formatTime(time.Now().UTC()), result.Run.ID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE messages SET status = 'failed', error_code = 'test_failed' WHERE id = ?`, result.AssistantMessage.ID).Error
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunBeginTurnCreatesAtomicPendingAndRetriesBeforeBusy(t *testing.T) {
	db, ctx, r := runFixture(t)
	input := runInput("client")
	input.ConversationID, input.ClientMessageID, input.Text = " entry-tx ", " client ", " hello user "
	result, err := r.BeginTurn(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.Run == nil || len(result.History) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	run := result.Run
	if err := agentrun.ValidateRun(*run); err != nil {
		t.Fatal(err)
	}
	if run.ConversationID != "entry-tx" || run.ClientMessageID != "client" || run.BaseEntryID != nil || run.BaseHeadVersion != 0 || run.HeadVersion != 1 ||
		run.Status != agentrun.StatusPending || run.StartedAt != nil || run.LastEntryID != run.UserEntryID || run.LastClosedEntryID != run.UserEntryID ||
		run.UserMessageID != result.UserMessage.ID || run.AssistantMessageID != result.AssistantMessage.ID || result.UserMessage.Content != input.Text ||
		result.UserMessage.Sequence != 1 || result.AssistantMessage.Sequence != 2 {
		t.Fatalf("invalid pending run/result: %+v %+v", run, result)
	}
	path, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", UseActiveLeaf: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Entries) != 1 || path.Entries[0].ID != run.UserEntryID || *path.Entries[0].Message.Content[0].Text != input.Text {
		t.Fatalf("unexpected path: %+v", path)
	}
	assertRunCounts(t, ctx, db, 2, 1, 1, 1)

	retryInput := runInput("client")
	retryInput.Text = "different retry text must not replace original"
	retry, err := r.BeginTurn(ctx, retryInput)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Reused || retry.Run == nil || !reflect.DeepEqual(retry.Run, run) || retry.UserMessage.ID != result.UserMessage.ID || retry.UserMessage.Content != input.Text {
		t.Fatalf("invalid retry: %+v", retry)
	}
	if got, err := r.BeginTurn(ctx, runInput("other")); !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(got, chat.BeginTurnResult{}) {
		t.Fatalf("busy = %+v, %v", got, err)
	}
	assertRunCounts(t, ctx, db, 2, 1, 1, 1)

	// Reuse of a terminal turn must also win over a newer active Run/head.
	failPendingFixture(t, ctx, db, result)
	second, err := r.BeginTurn(ctx, runInput("second"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Run.BaseEntryID == nil || *second.Run.BaseEntryID != run.UserEntryID || second.Run.BaseHeadVersion != 1 || second.Run.HeadVersion != 2 ||
		len(second.History) != 1 || second.History[0].ID != result.UserMessage.ID || second.UserMessage.Sequence != 3 {
		t.Fatalf("unexpected second run: %+v", second)
	}
	retry, err = r.BeginTurn(ctx, runInput("client"))
	if err != nil || retry.Run.Status != agentrun.StatusFailed || !retry.Reused {
		t.Fatalf("terminal retry = %+v, %v", retry, err)
	}
	assertRunCounts(t, ctx, db, 4, 2, 1, 2)
	*second.Run.BaseEntryID = "caller mutation"
	secondRetry, err := r.BeginTurn(ctx, runInput("second"))
	if err != nil || *secondRetry.Run.BaseEntryID != run.UserEntryID {
		t.Fatalf("retry aliases result: %+v, %v", secondRetry, err)
	}
}

func TestAgentRunLegacyRetryDoesNotBackfillAndWinsOverBusy(t *testing.T) {
	db, ctx, r := runFixture(t)
	old, err := r.turns.BeginTurn(ctx, runInput("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	reused, err := r.BeginTurn(ctx, runInput("legacy"))
	if err != nil || !reused.Reused || reused.Run != nil || reused.UserMessage.ID != old.UserMessage.ID {
		t.Fatalf("legacy retry = %+v, %v", reused, err)
	}
	if _, err := r.BeginTurn(ctx, runInput("new")); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("legacy busy = %v", err)
	}
	assertRunCounts(t, ctx, db, 2, 0, 0, 0)
	if _, err := r.turns.FinishAssistant(ctx, chat.FinishAssistantInput{AssistantMessageID: old.AssistantMessage.ID, Status: conversation.MessageStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BeginTurn(ctx, runInput("new")); err != nil {
		t.Fatal(err)
	}
	reused, err = r.BeginTurn(ctx, runInput("legacy"))
	if err != nil || !reused.Reused || reused.Run != nil || len(reused.History) != 0 {
		t.Fatalf("legacy retry while new run busy = %+v, %v", reused, err)
	}
	assertRunCounts(t, ctx, db, 4, 1, 1, 1)
}

func TestAgentRunConcurrentClientsAndConversationIsolation(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(map[bool]string{true: "same key", false: "different keys"}[sameKey], func(t *testing.T) {
			db, ctx, r := runFixture(t)
			var wg sync.WaitGroup
			results := make([]chat.BeginTurnResult, 8)
			errs := make([]error, 8)
			for i := range results {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					key := "client"
					if !sameKey {
						key += string(rune('a' + i))
					}
					results[i], errs[i] = r.BeginTurn(ctx, runInput(key))
				}(i)
			}
			wg.Wait()
			created := 0
			var id agentrun.RunID
			for i, result := range results {
				if errs[i] != nil {
					if sameKey || !errors.Is(errs[i], domainerr.ErrConflict) {
						t.Fatalf("unexpected error: %v", errs[i])
					}
					continue
				}
				if !result.Reused {
					created++
				}
				if id != "" && result.Run.ID != id {
					t.Fatal("retry returned another run")
				}
				id = result.Run.ID
			}
			if created != 1 {
				t.Fatalf("created = %d", created)
			}
			assertRunCounts(t, ctx, db, 2, 1, 1, 1)
			now := time.Now().UTC()
			if err := NewConversationRepository(db).Create(ctx, conversation.Conversation{ID: "other", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			input := runInput("client")
			input.ConversationID = "other"
			if result, err := r.BeginTurn(ctx, input); err != nil || result.Run.ConversationID != "other" {
				t.Fatalf("isolated run = %+v, %v", result, err)
			}
			assertRunCounts(t, ctx, db, 4, 2, 2, 2)
		})
	}
}

func TestAgentRunBeginTurnRollsBackEveryWriteFailure(t *testing.T) {
	for _, failure := range []struct{ name, sql string }{
		{"entry insert", `CREATE TRIGGER fail_insert BEFORE INSERT ON conversation_entry_nodes BEGIN SELECT RAISE(ABORT, 'entry failure'); END`},
		{"head CAS", `CREATE TRIGGER fail_cas BEFORE UPDATE ON conversation_entry_heads BEGIN SELECT RAISE(IGNORE); END`},
		{"user insert", `CREATE TRIGGER fail_insert BEFORE INSERT ON messages WHEN NEW.role = 'user' BEGIN SELECT RAISE(ABORT, 'user failure'); END`},
		{"assistant insert", `CREATE TRIGGER fail_insert BEFORE INSERT ON messages WHEN NEW.role = 'assistant' BEGIN SELECT RAISE(ABORT, 'assistant failure'); END`},
		{"conversation update", `CREATE TRIGGER fail_update BEFORE UPDATE ON conversations BEGIN SELECT RAISE(ABORT, 'conversation failure'); END`},
		{"run insert", `CREATE TRIGGER fail_insert BEFORE INSERT ON agent_runs BEGIN SELECT RAISE(ABORT, 'run failure'); END`},
		{"commit", `CREATE TRIGGER fail_commit AFTER INSERT ON agent_runs BEGIN UPDATE agent_runs SET last_entry_id = 'missing' WHERE id = NEW.id; END`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			before, err := NewConversationRepository(db).GetByID(ctx, "entry-tx")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, failure.sql); err != nil {
				t.Fatal(err)
			}
			result, err := r.BeginTurn(ctx, runInput("client"))
			if err == nil || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
				t.Fatalf("failed write leaked result: %+v %v", result, err)
			}
			if failure.name == "commit" {
				if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("expected public deferred FK COMMIT failure: %v", err)
				}
				assertAgentRunCommitFailure(t, ctx, db, r, runInput("client"))
			}
			assertRunCounts(t, ctx, db, 0, 0, 0, 0)
			after, err := NewConversationRepository(db).GetByID(ctx, "entry-tx")
			if err != nil {
				t.Fatal(err)
			}
			if !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatal("conversation timestamp survived rollback")
			}
			var trigger string
			if err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'fail_%'`).Scan(&trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "DROP TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := r.BeginTurn(ctx, runInput("client")); err != nil {
				t.Fatalf("retry after failure: %v", err)
			}
			assertRunCounts(t, ctx, db, 2, 1, 1, 1)
		})
	}
}

// Confirm the injected deferred FK permits every mutation and fails at COMMIT.
func assertAgentRunCommitFailure(t *testing.T, ctx context.Context, db *storage.DB, r *AgentRunRepository, input chat.BeginTurnInput) {
	t.Helper()
	callbackSucceeded := false
	err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		result, err := r.beginTurnTx(ctx, tx, input)
		if err != nil {
			return err
		}
		if result.Run == nil || result.Reused {
			t.Fatalf("expected provisional new Run before COMMIT: %+v", result)
		}
		callbackSucceeded = true
		return nil
	})
	if !callbackSucceeded || err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("expected deferred FK failure after successful callback: succeeded=%t, err=%v", callbackSucceeded, err)
	}
}

func TestAgentRunInputAndCorruptionAreReadOnly(t *testing.T) {
	for _, test := range []struct {
		name  string
		input chat.BeginTurnInput
		want  error
	}{
		{"conversation", chat.BeginTurnInput{ClientMessageID: "client", Text: "text"}, domainerr.ErrInvalidInput},
		{"client", chat.BeginTurnInput{ConversationID: "entry-tx", Text: "text"}, domainerr.ErrInvalidInput},
		{"text", chat.BeginTurnInput{ConversationID: "entry-tx", ClientMessageID: "client", Text: " "}, domainerr.ErrInvalidInput},
		{"missing conversation", chat.BeginTurnInput{ConversationID: "missing", ClientMessageID: "client", Text: "text"}, domainerr.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			result, err := r.BeginTurn(ctx, test.input)
			if !errors.Is(err, test.want) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
				t.Fatalf("result = %+v, %v", result, err)
			}
			assertRunCounts(t, ctx, db, 0, 0, 0, 0)
		})
	}
	for _, corruption := range []struct{ name, sql string }{
		{"wrong client", `UPDATE agent_runs SET client_message_id = 'moved'`},
		{"wrong message role", `UPDATE messages SET role = 'assistant' WHERE role = 'user'`},
		{"wrong pair sequence", `UPDATE messages SET sequence = 8 WHERE role = 'assistant'`},
		{"wrong entry text", `UPDATE messages SET content = 'changed' WHERE role = 'user'`},
		{"status mismatch", `UPDATE messages SET status = 'failed' WHERE role = 'assistant'`},
		{"invalid run timestamp", `UPDATE agent_runs SET created_at = 'invalid'`},
	} {
		t.Run(corruption.name, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			if _, err := r.BeginTurn(ctx, runInput("client")); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, corruption.sql); err != nil {
				t.Fatal(err)
			}
			input := runInput("client")
			if corruption.name == "wrong client" {
				input.ClientMessageID = "moved"
			}
			if result, err := r.BeginTurn(ctx, input); !errors.Is(err, conversation.ErrEntryCorrupt) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
				t.Fatalf("corruption = %+v, %v", result, err)
			}
			assertRunCounts(t, ctx, db, 2, 1, 1, 1)
		})
	}
}

func TestAgentRunBaseHeadBoundaries(t *testing.T) {
	for _, kind := range []string{"closed", "reset", "open", "corrupt payload", "missing head", "capacity"} {
		t.Run(kind, func(t *testing.T) {
			db, ctx, r := runFixture(t)
			root, err := r.tree.Append(ctx, conversation.AppendEntryInput{ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser)})
			if err != nil {
				t.Fatal(err)
			}
			head := root.Head
			switch kind {
			case "closed":
				answer, err := r.tree.Append(ctx, conversation.AppendEntryInput{ConversationID: "entry-tx", ExpectedHead: entryTxToken(head), Message: entryTxMessage(conversation.EntryRoleAssistant)})
				if err != nil {
					t.Fatal(err)
				}
				head = answer.Head
			case "reset":
				head, err = r.tree.MoveHead(ctx, conversation.MoveHeadInput{ConversationID: "entry-tx", ExpectedHead: entryTxToken(head)})
				if err != nil {
					t.Fatal(err)
				}
			case "open":
				policy := conversation.ToolPersistencePolicy{ToolName: "run-test-search", Version: "v1", ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 100}}, MaxPreviewBytes: 100}
				if err := conversation.RegisterToolPolicy(policy); err != nil {
					t.Fatal(err)
				}
				call, err := conversation.ProjectSafeToolCall(policy, "call-1", json.RawMessage(`{"q":"one"}`))
				if err != nil {
					t.Fatal(err)
				}
				_, err = r.tree.Append(ctx, conversation.AppendEntryInput{ConversationID: "entry-tx", ExpectedHead: entryTxToken(head), Message: conversation.EntryMessage{
					Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools, Content: []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}},
				}})
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt payload":
				if _, err := db.ExecContext(ctx, `DROP TRIGGER entry_nodes_no_update`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `UPDATE conversation_entry_nodes SET payload_json = '{}'`); err != nil {
					t.Fatal(err)
				}
			case "missing head":
				if _, err := db.ExecContext(ctx, `DELETE FROM conversation_entry_heads`); err != nil {
					t.Fatal(err)
				}
			case "capacity":
				r.tree.maxPathEntries = 1
			}
			result, err := r.BeginTurn(ctx, runInput("client"))
			if kind == "closed" || kind == "reset" {
				if err != nil {
					t.Fatal(err)
				}
				if !sameEntryID(result.Run.BaseEntryID, head.ActiveLeafID) || result.Run.BaseHeadVersion != head.Version || result.Run.HeadVersion != head.Version+1 {
					t.Fatalf("unexpected base: %+v", result.Run)
				}
				if _, err := r.BeginTurn(ctx, runInput("client")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err == nil || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
					t.Fatalf("unsafe base accepted: %+v, %v", result, err)
				}
				if kind == "open" && !errors.Is(err, domainerr.ErrConflict) {
					t.Fatalf("open head = %v", err)
				}
				if (kind == "corrupt payload" || kind == "missing head") && !errors.Is(err, conversation.ErrEntryCorrupt) {
					t.Fatalf("corrupt head = %v", err)
				}
				if kind == "capacity" && !errors.Is(err, conversation.ErrEntryCapacity) {
					t.Fatalf("capacity = %v", err)
				}
				entries, heads := 1, 1
				if kind == "open" {
					entries = 2
				}
				if kind == "missing head" {
					heads = 0
				}
				assertRunCounts(t, ctx, db, 0, entries, heads, 0)
			}
		})
	}
}

func TestAgentRunExistingHeadRollback(t *testing.T) {
	db, ctx, r := runFixture(t)
	first, err := r.BeginTurn(ctx, runInput("first"))
	if err != nil {
		t.Fatal(err)
	}
	failPendingFixture(t, ctx, db, first)
	before, err := r.tree.GetHead(ctx, "entry-tx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_commit AFTER INSERT ON agent_runs BEGIN UPDATE agent_runs SET last_entry_id = 'missing' WHERE id = NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	if result, err := r.BeginTurn(ctx, runInput("second")); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
		t.Fatalf("failed commit = %+v, %v", result, err)
	}
	assertAgentRunCommitFailure(t, ctx, db, r, runInput("second"))
	assertRunCounts(t, ctx, db, 2, 1, 1, 1)
	after, err := r.tree.GetHead(ctx, "entry-tx")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("head changed on rollback: %+v %+v", before, after)
	}
}

func TestAgentRunTerminalRetryValidatesRewindVersionAndClosedPoint(t *testing.T) {
	db, ctx, r := runFixture(t)
	first, err := r.BeginTurn(ctx, runInput("first"))
	if err != nil {
		t.Fatal(err)
	}
	failPendingFixture(t, ctx, db, first)
	second, err := r.BeginTurn(ctx, runInput("second"))
	if err != nil {
		t.Fatal(err)
	}
	// Lifecycle writes are not implemented in this slice. Construct their
	// persisted shape: the same user owns an unresolved assistant tool call;
	// failure preserves that fact and atomically rewinds the head to the user.
	policy := conversation.ToolPersistencePolicy{
		ToolName: "run-test-rewind", Version: "v1",
		ArgumentFields:  []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 100}},
		MaxPreviewBytes: 100,
	}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	call, err := conversation.ProjectSafeToolCall(policy, "call-1", json.RawMessage(`{"q":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	callEntry, err := r.tree.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "entry-tx",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &second.Run.UserEntryID, Version: second.Run.HeadVersion},
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var rewound conversation.EntryHead
	err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		rewound, err = r.tree.moveHeadTx(ctx, tx, conversation.MoveHeadInput{
			ConversationID: "entry-tx", ExpectedHead: entryTxToken(callEntry.Head), TargetID: &second.Run.UserEntryID,
		})
		if err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE agent_runs SET status = 'failed', error_code = 'model_failed', started_at = ?, finished_at = ?, last_entry_id = ?, last_closed_entry_id = ?, head_version = ? WHERE id = ?`,
			formatTime(second.Run.CreatedAt), formatTime(time.Now().UTC()), callEntry.Entry.ID, second.Run.UserEntryID, rewound.Version, second.Run.ID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE messages SET status = 'failed', error_code = 'model_failed' WHERE id = ?`, second.AssistantMessage.ID).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	valid, err := r.BeginTurn(ctx, runInput("second"))
	if err != nil || !valid.Reused || valid.Run == nil {
		t.Fatalf("valid rewind retry = %+v, %v", valid, err)
	}
	if valid.Run.UserEntryID != second.Run.UserEntryID || valid.Run.LastEntryID != callEntry.Entry.ID ||
		valid.Run.LastClosedEntryID != second.Run.UserEntryID || valid.Run.HeadVersion != callEntry.Head.Version+1 ||
		valid.Run.Status != agentrun.StatusFailed || valid.Run.StartedAt == nil || valid.Run.FinalEntryID != nil ||
		valid.UserMessage.ID != second.UserMessage.ID || valid.AssistantMessage.ID != second.AssistantMessage.ID {
		t.Fatalf("retry changed failed run identity or facts: %+v", valid)
	}
	path, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", LeafID: &callEntry.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Entries) != 3 || !reflect.DeepEqual(path.Entries[2], callEntry.Entry) ||
		path.Entries[1].ID != second.Run.UserEntryID || conversation.IsClosedBranchPoint(path.Entries) {
		t.Fatalf("failed tool branch was changed or closed: %+v", path)
	}
	assertHead := func() {
		t.Helper()
		head, err := r.tree.GetHead(ctx, "entry-tx")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(head, rewound) || !sameEntryID(head.ActiveLeafID, &second.Run.UserEntryID) {
			t.Fatalf("retry changed rewound head: %+v, want %+v", head, rewound)
		}
		assertRunCounts(t, ctx, db, 4, 3, 1, 2)
	}
	assertHead()
	if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET head_version = ? WHERE id = ?`, callEntry.Head.Version, second.Run.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := r.BeginTurn(ctx, runInput("second")); !errors.Is(err, conversation.ErrEntryCorrupt) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
		t.Fatalf("bad rewind version = %+v, %v", result, err)
	}
	assertHead()
	for _, closedID := range []conversation.EntryID{first.Run.UserEntryID, callEntry.Entry.ID} {
		if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET head_version = ?, last_closed_entry_id = ? WHERE id = ?`, rewound.Version, closedID, second.Run.ID); err != nil {
			t.Fatal(err)
		}
		if result, err := r.BeginTurn(ctx, runInput("second")); !errors.Is(err, conversation.ErrEntryCorrupt) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
			t.Fatalf("invalid closed point %s = %+v, %v", closedID, result, err)
		}
		assertHead()
	}
	// Restore the valid terminal snapshot before checking a new client turn.
	if _, err := db.ExecContext(ctx, `UPDATE agent_runs SET head_version = ?, last_closed_entry_id = ? WHERE id = ?`, rewound.Version, second.Run.UserEntryID, second.Run.ID); err != nil {
		t.Fatal(err)
	}
	third, err := r.BeginTurn(ctx, runInput("third"))
	if err != nil {
		t.Fatal(err)
	}
	if third.Reused || third.Run == nil || !sameEntryID(third.Run.BaseEntryID, rewound.ActiveLeafID) ||
		third.Run.BaseHeadVersion != rewound.Version || third.Run.HeadVersion != rewound.Version+1 {
		t.Fatalf("new client did not continue from rewound head: %+v", third)
	}
	userEntry, err := r.tree.Get(ctx, conversation.EntryRef{ConversationID: "entry-tx", ID: third.Run.UserEntryID})
	if err != nil {
		t.Fatal(err)
	}
	if !sameEntryID(userEntry.ParentID, &second.Run.UserEntryID) {
		t.Fatalf("new user attached to wrong branch: %+v", userEntry)
	}
	beforeRetry, err := r.tree.GetHead(ctx, "entry-tx")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := r.BeginTurn(ctx, runInput("second"))
	if err != nil || !retry.Reused || !reflect.DeepEqual(retry.Run, valid.Run) {
		t.Fatalf("terminal retry with newer active Run = %+v, %v", retry, err)
	}
	afterRetry, err := r.tree.GetHead(ctx, "entry-tx")
	if err != nil || !reflect.DeepEqual(afterRetry, beforeRetry) {
		t.Fatalf("terminal retry changed newer active head: %+v, %v", afterRetry, err)
	}
	preserved, err := r.tree.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", LeafID: &callEntry.Entry.ID})
	if err != nil || !reflect.DeepEqual(preserved.Entries, path.Entries) {
		t.Fatalf("new client changed abandoned branch: %+v, %v", preserved, err)
	}
	assertRunCounts(t, ctx, db, 6, 4, 1, 3)
}

func TestAgentRunLegacyIncompletePairIsNotReplaced(t *testing.T) {
	db, ctx, r := runFixture(t)
	old, err := r.turns.BeginTurn(ctx, runInput("client"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, old.AssistantMessage.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := r.BeginTurn(ctx, runInput("client")); err == nil || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
		t.Fatalf("incomplete legacy = %+v, %v", result, err)
	}
	assertRunCounts(t, ctx, db, 1, 0, 0, 0)
}

func TestRecoverInterruptedFindsAndFailsActiveRuns(t *testing.T) {
	db, ctx, r := runFixture(t)
	now := time.Now().UTC()
	convRepo := NewConversationRepository(db)
	
	// Create conversations
	for _, id := range []string{"conv1", "conv2", "conv3"} {
		if err := convRepo.Create(ctx, conversation.Conversation{
			ID: id, Title: "test", CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	
	// Create a pending run in conversation 1
	input1 := chat.BeginTurnInput{ConversationID: "conv1", ClientMessageID: "client1", Text: "hello"}
	_, err := r.BeginTurn(ctx, input1)
	if err != nil {
		t.Fatal(err)
	}
	
	// Create a running run with tool call in conversation 2
	input2 := chat.BeginTurnInput{ConversationID: "conv2", ClientMessageID: "client2", Text: "hello"}
	turn2, err := r.BeginTurn(ctx, input2)
	if err != nil {
		t.Fatal(err)
	}
	run2, err := r.MarkRunning(ctx, turn2.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	
	// Simulate tool call to make it waiting_tool
	call := lifecycleCall(t)
	token, err := run2.ActiveHeadToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.CommitRunMessages(ctx, chat.CommitRunMessagesInput{
		RunID: run2.ID, ExpectedHead: token, Messages: []conversation.EntryMessage{call},
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Create a completed run in conversation 3 (should not be recovered)
	input3 := chat.BeginTurnInput{ConversationID: "conv3", ClientMessageID: "client3", Text: "hello"}
	turn3, err := r.BeginTurn(ctx, input3)
	if err != nil {
		t.Fatal(err)
	}
	run3, err := r.MarkRunning(ctx, turn3.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	token3, err := run3.ActiveHeadToken()
	if err != nil {
		t.Fatal(err)
	}
	final := entryTxMessage(conversation.EntryRoleAssistant)
	_, err = r.FinishAssistant(ctx, chat.FinishAssistantInput{
		AssistantMessageID: run3.AssistantMessageID,
		Status:             conversation.MessageStatusCompleted,
		ExpectedHead:       &token3,
		FinalMessage:       &final,
	})
	if err != nil {
		t.Fatal(err)
	}
	
	// Run recovery
	result, err := r.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	
	// Should recover 2 active runs (1 pending + 1 waiting_tool)
	if result.InterruptedRuns != 2 {
		t.Fatalf("recovered runs = %d, want 2", result.InterruptedRuns)
	}
	if result.LegacyOnlyMessages != 0 {
		t.Fatalf("recovered legacy = %d, want 0", result.LegacyOnlyMessages)
	}
	
	// Verify runs are now failed
	var failedCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_runs WHERE status = ?", agentrun.StatusFailed).Scan(&failedCount)
	if err != nil {
		t.Fatal(err)
	}
	if failedCount != 2 {
		t.Fatalf("failed run count = %d, want 2", failedCount)
	}
	
	// Verify associated assistant messages are also failed
	var failedMsgCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE role = ? AND status = ? AND error_code = ?",
		conversation.MessageRoleAssistant, conversation.MessageStatusFailed, "interrupted").Scan(&failedMsgCount)
	if err != nil {
		t.Fatal(err)
	}
	if failedMsgCount != 2 {
		t.Fatalf("failed message count = %d, want 2", failedMsgCount)
	}
	
	// Second recovery should find nothing
	result2, err := r.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result2.InterruptedRuns != 0 || result2.LegacyOnlyMessages != 0 {
		t.Fatalf("second recovery found runs: %+v", result2)
	}
}
