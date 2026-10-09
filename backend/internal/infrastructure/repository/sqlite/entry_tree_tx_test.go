package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func newEntryTxFixture(t *testing.T) (*storage.DB, context.Context, *EntryTreeRepository) {
	t.Helper()
	// A nested transaction/DB query would exhaust the single connection and
	// time out instead of leaving the test hung indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "entry-tx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := NewConversationRepository(db).Create(ctx, conversation.Conversation{
		ID: "entry-tx", Title: "transaction test", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := NewEntryTreeRepository(db, EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return db, ctx, repo
}

func entryTxMessage(role conversation.EntryRole) conversation.EntryMessage {
	text := "approved text"
	message := conversation.EntryMessage{
		Role: role, Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}},
	}
	if role == conversation.EntryRoleAssistant {
		message.StopReason = conversation.EntryStopNormal
	}
	return message
}

func entryTxToken(head conversation.EntryHead) conversation.HeadToken {
	return conversation.HeadToken{ActiveLeafID: head.ActiveLeafID, Version: head.Version}
}

func assertEntryTxCounts(t *testing.T, ctx context.Context, db *storage.DB, nodes, heads int) {
	t.Helper()
	for _, item := range []struct {
		table string
		want  int
	}{{"conversation_entry_nodes", nodes}, {"conversation_entry_heads", heads}} {
		var got int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+item.table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != item.want {
			t.Fatalf("%s count = %d, want %d", item.table, got, item.want)
		}
	}
}

func TestEntryTxAppendAndMoveShareCallerTransaction(t *testing.T) {
	db, ctx, repo := newEntryTxFixture(t)
	core := *repo
	core.db = nil // Tx cores must not use the repository connection.
	var root, answer, branch conversation.AppendEntryResult
	var moved conversation.EntryHead
	err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		root, err = core.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
		})
		if err != nil {
			return err
		}
		answer, err = core.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: "entry-tx", ExpectedHead: entryTxToken(root.Head), Message: entryTxMessage(conversation.EntryRoleAssistant),
		})
		if err != nil {
			return err
		}
		moved, err = core.moveHeadTx(ctx, tx, conversation.MoveHeadInput{
			ConversationID: "entry-tx", ExpectedHead: entryTxToken(answer.Head), TargetID: &root.Entry.ID,
		})
		if err != nil {
			return err
		}
		branch, err = core.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: "entry-tx", ExpectedHead: entryTxToken(moved), Message: entryTxMessage(conversation.EntryRoleUser),
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.Head.Version != 1 || answer.Head.Version != 2 || moved.Version != 3 || branch.Head.Version != 4 {
		t.Fatalf("unexpected versions: %d %d %d %d", root.Head.Version, answer.Head.Version, moved.Version, branch.Head.Version)
	}
	if branch.Entry.AppendIndex != 3 || branch.Entry.Depth != 1 || branch.Entry.ParentID == nil || *branch.Entry.ParentID != root.Entry.ID {
		t.Fatalf("unexpected branch: %+v", branch.Entry)
	}
	path, err := repo.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", UseActiveLeaf: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Entries) != 2 || path.Entries[0].ID != root.Entry.ID || path.Entries[1].ID != branch.Entry.ID {
		t.Fatalf("unexpected path: %+v", path)
	}
	if _, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "entry-tx", ID: answer.Entry.ID}); err != nil {
		t.Fatalf("abandoned branch must remain readable: %v", err)
	}
	assertEntryTxCounts(t, ctx, db, 3, 1)
}

func TestEntryTxLaterErrorRollsBackAllWrites(t *testing.T) {
	for _, failure := range []string{"caller", "append_conflict", "move_conflict", "move_missing_target"} {
		t.Run(failure, func(t *testing.T) {
			db, ctx, repo := newEntryTxFixture(t)
			injected := errors.New("later write failed")
			err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				root, err := repo.appendTx(ctx, tx, conversation.AppendEntryInput{
					ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
				})
				if err != nil {
					return err
				}
				switch failure {
				case "caller":
					return injected
				case "append_conflict":
					result, err := repo.appendTx(ctx, tx, conversation.AppendEntryInput{
						ConversationID: "entry-tx", Message: entryTxMessage(conversation.EntryRoleUser),
					})
					if !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
						t.Fatalf("failed helper returned a result: %+v", result)
					}
					return err
				default:
					input := conversation.MoveHeadInput{ConversationID: "entry-tx"}
					if failure == "move_missing_target" {
						missing := conversation.EntryID("e_missing")
						input.ExpectedHead = entryTxToken(root.Head)
						input.TargetID = &missing
					}
					result, err := repo.moveHeadTx(ctx, tx, input)
					if !reflect.DeepEqual(result, conversation.EntryHead{}) {
						t.Fatalf("failed helper returned a head: %+v", result)
					}
					return err
				}
			})
			if err == nil {
				t.Fatal("expected outer transaction failure")
			}
			if (failure == "append_conflict" || failure == "move_conflict") && !errors.Is(err, domainerr.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
			assertEntryTxCounts(t, ctx, db, 0, 0)
		})
	}
}

func TestEntryTxPayloadValidationCannotBeBypassed(t *testing.T) {
	for _, invalid := range []string{"structure", "capacity"} {
		t.Run(invalid, func(t *testing.T) {
			db, ctx, repo := newEntryTxFixture(t)
			message := conversation.EntryMessage{}
			if invalid == "capacity" {
				message = entryTxMessage(conversation.EntryRoleUser)
				repo.maxPayloadBytes = 1
			}
			err := db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
				result, err := repo.appendTx(ctx, tx, conversation.AppendEntryInput{ConversationID: "entry-tx", Message: message})
				if !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
					t.Fatalf("invalid payload returned a result: %+v", result)
				}
				return err
			})
			want := domainerr.ErrInvalidInput
			if invalid == "capacity" {
				want = conversation.ErrEntryCapacity
			}
			if !errors.Is(err, want) {
				t.Fatalf("expected %v, got %v", want, err)
			}
			assertEntryTxCounts(t, ctx, db, 0, 0)
		})
	}
}
