package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func newChatTxTestRepository(t *testing.T) (context.Context, *storage.DB, *ChatTurnRepository, conversation.Conversation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "chat-tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	record := conversation.Conversation{ID: "chat-tx", Title: "Tx core", CreatedAt: now, UpdatedAt: now}
	if err := NewConversationRepository(db).Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	return ctx, db, NewChatTurnRepository(db), record
}

func chatTxBeginInput(clientID string) chat.BeginTurnInput {
	return chat.BeginTurnInput{ConversationID: "chat-tx", ClientMessageID: clientID, Text: " user text "}
}

func assertChatTxMessage(t *testing.T, ctx context.Context, repo *ChatTurnRepository, want conversation.Message) {
	t.Helper()
	got, err := repo.GetMessage(ctx, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message = %+v, want %+v", got, want)
	}
}

func assertChatTxCount(t *testing.T, ctx context.Context, db *storage.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func TestChatTurnTxComposition(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "commit"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx, db, repo, originalConversation := newChatTxTestRepository(t)
			// A core without a DB proves that all operations use the supplied Tx.
			core := &ChatTurnRepository{}
			abort := errors.New("abort composed chat transaction")
			var first, second, third chat.BeginTurnResult
			err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				input := chatTxBeginInput(" client-1 ")
				input.ConversationID = " chat-tx "
				var err error
				first, err = core.beginTurnTx(ctx, tx, input)
				if err != nil {
					return err
				}
				if first.Reused || first.Run != nil || len(first.History) != 0 || first.History == nil || first.UserMessage.Sequence != 1 || first.AssistantMessage.Sequence != 2 || first.UserMessage.Content != input.Text || first.UserMessage.ClientMessageID != "client-1" {
					t.Fatalf("unexpected first pair: %+v", first)
				}
				for _, message := range []conversation.Message{first.UserMessage, first.AssistantMessage} {
					if !strings.HasPrefix(message.ID, "m_") || len(message.ID) != 34 {
						t.Fatalf("unexpected message ID: %q", message.ID)
					}
				}
				if first.UserMessage.ID == first.AssistantMessage.ID {
					t.Fatal("message IDs must be distinct")
				}
				for _, text := range []string{"answer", " "} {
					if err := core.appendAssistantTextTx(ctx, tx, first.AssistantMessage.ID, text); err != nil {
						return err
					}
				}
				retry := input
				retry.Text = "ignored on retry"
				reused, err := core.beginTurnTx(ctx, tx, retry)
				if err != nil {
					return err
				}
				if !reused.Reused || reused.UserMessage != first.UserMessage || reused.AssistantMessage.ID != first.AssistantMessage.ID || reused.AssistantMessage.Content != "answer " {
					t.Fatalf("retry while busy did not reuse pair: %+v", reused)
				}
				first.AssistantMessage, err = core.finishAssistantTx(ctx, tx, chat.FinishAssistantInput{
					AssistantMessageID: " " + first.AssistantMessage.ID + " ", Status: conversation.MessageStatusCompleted,
				})
				if err != nil {
					return err
				}
				second, err = core.beginTurnTx(ctx, tx, chatTxBeginInput("client-2"))
				if err != nil {
					return err
				}
				if second.UserMessage.Sequence != 3 || second.AssistantMessage.Sequence != 4 || !reflect.DeepEqual(second.History, []conversation.Message{first.UserMessage, first.AssistantMessage}) {
					t.Fatalf("unexpected second pair/history: %+v", second)
				}
				second.AssistantMessage, err = core.finishAssistantTx(ctx, tx, chat.FinishAssistantInput{
					AssistantMessageID: second.AssistantMessage.ID, Status: conversation.MessageStatusFailed,
					ErrorCode: "model_failed", ErrorMessage: "safe failure",
				})
				if err != nil {
					return err
				}
				third, err = core.beginTurnTx(ctx, tx, chatTxBeginInput("client-3"))
				if err != nil {
					return err
				}
				if third.UserMessage.Sequence != 5 || third.AssistantMessage.Sequence != 6 || !reflect.DeepEqual(third.History, []conversation.Message{first.UserMessage, first.AssistantMessage, second.UserMessage}) {
					t.Fatalf("failed assistant must be excluded from history: %+v", third)
				}
				if rollback {
					return abort
				}
				return nil
			})
			if rollback {
				if !errors.Is(err, abort) {
					t.Fatalf("expected outer rollback, got %v", err)
				}
				assertChatTxCount(t, ctx, db, "messages", 0)
				got, err := NewConversationRepository(db).GetByID(ctx, originalConversation.ID)
				if err != nil || !reflect.DeepEqual(got, originalConversation) {
					t.Fatalf("conversation changed after rollback: %+v, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertChatTxCount(t, ctx, db, "messages", 6)
			assertChatTxMessage(t, ctx, repo, first.AssistantMessage)
			assertChatTxMessage(t, ctx, repo, second.AssistantMessage)
			assertChatTxMessage(t, ctx, repo, third.AssistantMessage)
		})
	}
}

func TestChatTurnTxRollbackPreservesExistingTextAndStatus(t *testing.T) {
	ctx, db, repo, _ := newChatTxTestRepository(t)
	turn, err := repo.BeginTurn(ctx, chatTxBeginInput("client-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendAssistantText(ctx, turn.AssistantMessage.ID, "persisted"); err != nil {
		t.Fatal(err)
	}
	before, err := repo.GetMessage(ctx, turn.AssistantMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	abort := errors.New("abort after text and finish")
	err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if err := repo.appendAssistantTextTx(ctx, tx, before.ID, " discarded"); err != nil {
			return err
		}
		finished, err := repo.finishAssistantTx(ctx, tx, chat.FinishAssistantInput{
			AssistantMessageID: before.ID, Status: conversation.MessageStatusCancelled,
		})
		if err != nil {
			return err
		}
		if finished.Content != "persisted discarded" || finished.Status != conversation.MessageStatusCancelled {
			t.Fatalf("unexpected provisional result: %+v", finished)
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("expected rollback, got %v", err)
	}
	assertChatTxMessage(t, ctx, repo, before)
}

func TestChatTurnTxCommitFailure(t *testing.T) {
	for _, operation := range []string{"begin", "append", "finish"} {
		for _, public := range []bool{true, false} {
			name := operation + "/core"
			if public {
				name = operation + "/public"
			}
			t.Run(name, func(t *testing.T) {
				ctx, db, repo, originalConversation := newChatTxTestRepository(t)
				var before conversation.Message
				if operation != "begin" {
					turn, err := repo.BeginTurn(ctx, chatTxBeginInput("client-1"))
					if err != nil {
						t.Fatal(err)
					}
					if err := repo.AppendAssistantText(ctx, turn.AssistantMessage.ID, "persisted"); err != nil {
						t.Fatal(err)
					}
					before, err = repo.GetMessage(ctx, turn.AssistantMessage.ID)
					if err != nil {
						t.Fatal(err)
					}
					originalConversation, err = NewConversationRepository(db).GetByID(ctx, "chat-tx")
					if err != nil {
						t.Fatal(err)
					}
				}
				// This test-only deferred FK succeeds during mutation but fails at COMMIT.
				if _, err := db.ExecContext(ctx, `CREATE TABLE chat_tx_commit_probe (
					message_id TEXT REFERENCES messages(id) DEFERRABLE INITIALLY DEFERRED
				)`); err != nil {
					t.Fatal(err)
				}
				event := "UPDATE"
				if operation == "begin" {
					event = "INSERT"
				}
				if _, err := db.ExecContext(ctx, `CREATE TRIGGER inject_chat_tx_commit_failure AFTER `+event+` ON messages
					WHEN NEW.role = 'assistant' BEGIN
						INSERT INTO chat_tx_commit_probe VALUES ('missing-message');
					END`); err != nil {
					t.Fatal(err)
				}
				var beginResult chat.BeginTurnResult
				var finishResult conversation.Message
				finishInput := chat.FinishAssistantInput{AssistantMessageID: before.ID, Status: conversation.MessageStatusCompleted}
				var err error
				if public {
					switch operation {
					case "begin":
						beginResult, err = repo.BeginTurn(ctx, chatTxBeginInput("client-1"))
					case "append":
						err = repo.AppendAssistantText(ctx, before.ID, " discarded")
					case "finish":
						finishResult, err = repo.FinishAssistant(ctx, finishInput)
					}
					if !reflect.DeepEqual(beginResult, chat.BeginTurnResult{}) || !reflect.DeepEqual(finishResult, conversation.Message{}) {
						t.Fatalf("uncommitted public result returned: %+v, %+v", beginResult, finishResult)
					}
				} else {
					callbackSucceeded := false
					err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
						var err error
						switch operation {
						case "begin":
							beginResult, err = repo.beginTurnTx(ctx, tx, chatTxBeginInput("client-1"))
						case "append":
							err = repo.appendAssistantTextTx(ctx, tx, before.ID, " discarded")
						case "finish":
							finishResult, err = repo.finishAssistantTx(ctx, tx, finishInput)
						}
						callbackSucceeded = err == nil
						return err
					})
					if !callbackSucceeded {
						t.Fatalf("failure must occur at COMMIT, not in core: %v", err)
					}
				}
				if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("expected deferred FK COMMIT failure, got %v", err)
				}
				assertChatTxCount(t, ctx, db, "chat_tx_commit_probe", 0)
				if operation == "begin" {
					assertChatTxCount(t, ctx, db, "messages", 0)
				} else {
					assertChatTxCount(t, ctx, db, "messages", 2)
					assertChatTxMessage(t, ctx, repo, before)
				}
				got, err := NewConversationRepository(db).GetByID(ctx, "chat-tx")
				if err != nil || !reflect.DeepEqual(got, originalConversation) {
					t.Fatalf("conversation changed after COMMIT failure: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestChatTurnTxMutationErrorAbortsOuterTransaction(t *testing.T) {
	for _, stage := range []string{"assistant_insert", "begin_touch", "finish_touch"} {
		t.Run(stage, func(t *testing.T) {
			ctx, db, repo, originalConversation := newChatTxTestRepository(t)
			var before conversation.Message
			if stage == "finish_touch" {
				turn, err := repo.BeginTurn(ctx, chatTxBeginInput("client-1"))
				if err != nil {
					t.Fatal(err)
				}
				before = turn.AssistantMessage
				originalConversation = turn.Conversation
			}
			trigger := `CREATE TRIGGER fail_chat_tx_mutation BEFORE UPDATE ON conversations
				BEGIN SELECT RAISE(ABORT, 'injected chat mutation failure'); END`
			if stage == "assistant_insert" {
				trigger = `CREATE TRIGGER fail_chat_tx_mutation BEFORE INSERT ON messages
					WHEN NEW.role = 'assistant'
					BEGIN SELECT RAISE(ABORT, 'injected chat mutation failure'); END`
			}
			if _, err := db.ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				if stage == "finish_touch" {
					if err := repo.appendAssistantTextTx(ctx, tx, before.ID, "discarded"); err != nil {
						return err
					}
					result, err := repo.finishAssistantTx(ctx, tx, chat.FinishAssistantInput{
						AssistantMessageID: before.ID, Status: conversation.MessageStatusCompleted,
					})
					if !reflect.DeepEqual(result, conversation.Message{}) {
						t.Fatalf("failed core returned message: %+v", result)
					}
					return err
				}
				result, err := repo.beginTurnTx(ctx, tx, chatTxBeginInput("client-1"))
				if !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
					t.Fatalf("failed core returned pair: %+v", result)
				}
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "injected chat mutation failure") {
				t.Fatalf("expected mutation failure, got %v", err)
			}
			if stage == "finish_touch" {
				assertChatTxMessage(t, ctx, repo, before)
				assertChatTxCount(t, ctx, db, "messages", 2)
			} else {
				assertChatTxCount(t, ctx, db, "messages", 0)
			}
			got, err := NewConversationRepository(db).GetByID(ctx, "chat-tx")
			if err != nil || !reflect.DeepEqual(got, originalConversation) {
				t.Fatalf("conversation changed after mutation failure: %+v, %v", got, err)
			}
		})
	}
}

func TestChatTurnTxInputValidationCannotBeBypassed(t *testing.T) {
	// No DB/Tx is available, and the context is cancelled: invalid input must
	// still win before opening a transaction or issuing SQL in the core.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &ChatTurnRepository{}
	for _, input := range []chat.BeginTurnInput{
		{}, {ConversationID: " ", ClientMessageID: "client", Text: "text"},
		{ConversationID: "chat-tx", ClientMessageID: " ", Text: "text"},
		{ConversationID: "chat-tx", ClientMessageID: "client", Text: " \t\n"},
	} {
		for _, public := range []bool{true, false} {
			var result chat.BeginTurnResult
			var err error
			if public {
				result, err = repo.BeginTurn(ctx, input)
			} else {
				result, err = repo.beginTurnTx(ctx, nil, input)
			}
			if !errors.Is(err, domainerr.ErrInvalidInput) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
				t.Fatalf("BeginTurn validation (%+v, public=%t) = %+v, %v", input, public, result, err)
			}
		}
	}
	for _, input := range []chat.FinishAssistantInput{
		{}, {AssistantMessageID: " ", Status: conversation.MessageStatusCompleted},
		{AssistantMessageID: "assistant", Status: conversation.MessageStatusStreaming},
		{AssistantMessageID: "assistant", Status: "invalid"},
		{AssistantMessageID: "assistant", Status: conversation.MessageStatusCompleted, ErrorCode: "error"},
		{AssistantMessageID: "assistant", Status: conversation.MessageStatusCancelled, ErrorMessage: "error"},
	} {
		for _, public := range []bool{true, false} {
			var result conversation.Message
			var err error
			if public {
				result, err = repo.FinishAssistant(ctx, input)
			} else {
				result, err = repo.finishAssistantTx(ctx, nil, input)
			}
			if !errors.Is(err, domainerr.ErrInvalidInput) || !reflect.DeepEqual(result, conversation.Message{}) {
				t.Fatalf("FinishAssistant validation (%+v, public=%t) = %+v, %v", input, public, result, err)
			}
		}
	}
	for _, input := range [][2]string{{"", "text"}, {" \t", "text"}, {"assistant", ""}} {
		if err := repo.AppendAssistantText(ctx, input[0], input[1]); !errors.Is(err, domainerr.ErrInvalidInput) {
			t.Fatalf("public text validation = %v", err)
		}
		if err := repo.appendAssistantTextTx(ctx, nil, input[0], input[1]); !errors.Is(err, domainerr.ErrInvalidInput) {
			t.Fatalf("core text validation = %v", err)
		}
	}
}

func TestChatTurnTxLegacyTerminalAndBusySemantics(t *testing.T) {
	for _, status := range []conversation.MessageStatus{conversation.MessageStatusCompleted, conversation.MessageStatusFailed, conversation.MessageStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			ctx, db, repo, _ := newChatTxTestRepository(t)
			turn, err := repo.BeginTurn(ctx, chatTxBeginInput("client-1"))
			if err != nil {
				t.Fatal(err)
			}
			err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				result, err := repo.beginTurnTx(ctx, tx, chatTxBeginInput("client-2"))
				if !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(result, chat.BeginTurnResult{}) {
					t.Fatalf("new client while busy = %+v, %v", result, err)
				}
				return err
			})
			if !errors.Is(err, domainerr.ErrConflict) {
				t.Fatal(err)
			}
			input := chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: status}
			if status == conversation.MessageStatusFailed {
				input.ErrorCode, input.ErrorMessage = "model_failed", "safe failure"
			}
			finished, err := repo.FinishAssistant(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			for _, retryStatus := range []conversation.MessageStatus{conversation.MessageStatusCompleted, conversation.MessageStatusFailed, conversation.MessageStatusCancelled} {
				err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
					result, err := repo.finishAssistantTx(ctx, tx, chat.FinishAssistantInput{AssistantMessageID: finished.ID, Status: retryStatus})
					if status == conversation.MessageStatusCancelled && retryStatus == status {
						if err != nil || result != finished {
							t.Fatalf("repeated cancellation = %+v, %v", result, err)
						}
					} else if !errors.Is(err, domainerr.ErrConflict) || !reflect.DeepEqual(result, conversation.Message{}) {
						t.Fatalf("terminal competition = %+v, %v", result, err)
					}
					return err
				})
				if err != nil && !errors.Is(err, domainerr.ErrConflict) {
					t.Fatal(err)
				}
			}
			err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				return repo.appendAssistantTextTx(ctx, tx, finished.ID, "late")
			})
			if !errors.Is(err, domainerr.ErrConflict) {
				t.Fatalf("late text = %v", err)
			}
			assertChatTxMessage(t, ctx, repo, finished)
			assertChatTxCount(t, ctx, db, "messages", 2)
		})
	}
}
