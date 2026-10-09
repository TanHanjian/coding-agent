package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestEntryAndChatTxComposition(t *testing.T) {
	for _, failure := range []string{"none", "entry", "message", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, ctx, entries := newEntryTxFixture(t)
			messages := NewChatTurnRepository(db)
			before, err := NewConversationRepository(db).GetByID(ctx, "entry-tx")
			if err != nil {
				t.Fatal(err)
			}
			if failure == "commit" {
				if _, err := db.ExecContext(ctx, `CREATE TABLE entry_chat_commit_probe (
					entry_id TEXT REFERENCES conversation_entry_nodes(id) DEFERRABLE INITIALLY DEFERRED
				)`); err != nil {
					t.Fatal(err)
				}
			}
			var turn chat.BeginTurnResult
			var answer conversation.AppendEntryResult
			var finished conversation.Message
			callbackSucceeded := false
			err = db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				var err error
				turn, err = messages.beginTurnTx(ctx, tx, chat.BeginTurnInput{
					ConversationID: "entry-tx", ClientMessageID: "client-1", Text: "user text",
				})
				if err != nil {
					return err
				}
				root, err := entries.appendTx(ctx, tx, conversation.AppendEntryInput{
					ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
				})
				if err != nil {
					return err
				}
				if err := messages.appendAssistantTextTx(ctx, tx, turn.AssistantMessage.ID, "answer"); err != nil {
					return err
				}
				input := conversation.AppendEntryInput{
					ConversationID: "entry-tx", ExpectedHead: entryTxToken(root.Head), Message: entryTxMessage(conversation.EntryRoleAssistant),
				}
				if failure == "entry" {
					input.ExpectedHead = conversation.HeadToken{}
				}
				answer, err = entries.appendTx(ctx, tx, input)
				if err != nil {
					return err
				}
				finish := chat.FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusCompleted}
				if failure == "message" {
					finish.AssistantMessageID = "missing-assistant"
				}
				finished, err = messages.finishAssistantTx(ctx, tx, finish)
				if err != nil {
					return err
				}
				if failure == "commit" {
					if err := tx.Exec(`INSERT INTO entry_chat_commit_probe VALUES ('missing-entry')`).Error; err != nil {
						return err
					}
				}
				callbackSucceeded = true
				return nil
			})
			if failure != "none" {
				if err == nil {
					t.Fatal("expected composed transaction failure")
				}
				if failure == "commit" {
					if !callbackSucceeded || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
						t.Fatalf("expected failure only at COMMIT, got %v", err)
					}
					assertChatTxCount(t, ctx, db, "entry_chat_commit_probe", 0)
				}
				assertEntryTxCounts(t, ctx, db, 0, 0)
				assertChatTxCount(t, ctx, db, "messages", 0)
				after, err := NewConversationRepository(db).GetByID(ctx, "entry-tx")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("conversation changed on rollback: %+v, %v", after, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertEntryTxCounts(t, ctx, db, 2, 1)
			assertChatTxCount(t, ctx, db, "messages", 2)
			assertChatTxMessage(t, ctx, messages, finished)
			if finished.Content != "answer" || finished.Status != conversation.MessageStatusCompleted {
				t.Fatalf("unexpected final message: %+v", finished)
			}
			head, err := entries.GetHead(ctx, "entry-tx")
			if err != nil || !reflect.DeepEqual(head, answer.Head) {
				t.Fatalf("unexpected committed head: %+v, %v", head, err)
			}
		})
	}
}

func TestEntryTxPublicCommitFailureReturnsZeroResult(t *testing.T) {
	for _, operation := range []string{"append", "move"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, repo := newEntryTxFixture(t)
			var before conversation.EntryHead
			if operation == "move" {
				root, err := repo.Append(ctx, conversation.AppendEntryInput{
					ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
				})
				if err != nil {
					t.Fatal(err)
				}
				before = root.Head
			}
			if _, err := db.ExecContext(ctx, `CREATE TABLE entry_tx_commit_probe (
				entry_id TEXT REFERENCES conversation_entry_nodes(id) DEFERRABLE INITIALLY DEFERRED
			)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_entry_tx_commit AFTER UPDATE ON conversation_entry_heads
				BEGIN INSERT INTO entry_tx_commit_probe VALUES ('missing-entry'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "append" {
				var result conversation.AppendEntryResult
				result, err = repo.Append(ctx, conversation.AppendEntryInput{
					ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
				})
				if !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
					t.Fatalf("uncommitted result returned: %+v", result)
				}
			} else {
				var result conversation.EntryHead
				result, err = repo.MoveHead(ctx, conversation.MoveHeadInput{
					ConversationID: "entry-tx", ExpectedHead: entryTxToken(before),
				})
				if !reflect.DeepEqual(result, conversation.EntryHead{}) {
					t.Fatalf("uncommitted head returned: %+v", result)
				}
			}
			if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatalf("expected deferred FK COMMIT failure, got %v", err)
			}
			assertChatTxCount(t, ctx, db, "entry_tx_commit_probe", 0)
			if operation == "append" {
				assertEntryTxCounts(t, ctx, db, 0, 0)
			} else {
				assertEntryTxCounts(t, ctx, db, 1, 1)
				after, err := repo.GetHead(ctx, "entry-tx")
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("head changed on failed COMMIT: %+v, %v", after, err)
				}
			}
		})
	}
}

func TestEntryTxAppendMutationErrorRollsBackNode(t *testing.T) {
	db, ctx, repo := newEntryTxFixture(t)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_entry_head_update BEFORE UPDATE ON conversation_entry_heads
		BEGIN SELECT RAISE(ABORT, 'injected head update failure'); END`); err != nil {
		t.Fatal(err)
	}
	err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		result, err := repo.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
		})
		if !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
			t.Fatalf("failed core returned entry: %+v", result)
		}
		if err == nil {
			return errors.New("expected failure after node insertion")
		}
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "injected head update failure") {
		t.Fatalf("expected head update failure, got %v", err)
	}
	assertEntryTxCounts(t, ctx, db, 0, 0)
}
