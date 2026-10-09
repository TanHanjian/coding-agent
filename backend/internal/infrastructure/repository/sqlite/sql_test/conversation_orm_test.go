package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func conversationORMExec(t *testing.T, ctx context.Context, db *storage.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func conversationORMFixture(t *testing.T, ctx context.Context, db *storage.DB, id string) {
	t.Helper()
	conversationORMExec(t, ctx, db, `INSERT INTO conversations (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		id, "fixture title", "2020-01-02T03:04:05.123456789Z", "2020-01-02T03:04:05.123456789Z")
}

func messageORMFixture(t *testing.T, ctx context.Context, db *storage.DB, id, conversationID string, sequence int) {
	t.Helper()
	// Omit the optional text fields to exercise historical schema defaults.
	conversationORMExec(t, ctx, db, `INSERT INTO messages (id, conversation_id, sequence, role, status, created_at, updated_at)
		VALUES (?, ?, ?, 'assistant', 'completed', ?, ?)`, id, conversationID, sequence,
		"2020-01-02T03:04:05.123456789Z", "2020-01-02T03:04:05.123456789Z")
}

func TestConversationORMSQLFixtureRoundTrip(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	repo := sqlite.NewConversationRepository(db)
	const createdText = "2024-02-03T04:05:06.123456789+08:00"
	const updatedText = "2024-02-03T04:06:07.987654321+08:00"
	conversationORMExec(t, ctx, db, `INSERT INTO conversations (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		"legacy", "旧会话", createdText, updatedText)
	createdAt, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedText)
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "legacy" || got.Title != "旧会话" || !got.CreatedAt.Equal(createdAt) || !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("SQL fixture changed: %+v", got)
	}

	for _, record := range []conversation.Conversation{
		{ID: "roundtrip", Title: got.Title, CreatedAt: got.CreatedAt, UpdatedAt: got.UpdatedAt},
		{ID: "zero"},
	} {
		if err := repo.Create(ctx, record); err != nil {
			t.Fatal(err)
		}
		var title, created, updated, titleType, createdType, updatedType string
		if err := db.QueryRowContext(ctx, `SELECT title, created_at, updated_at, typeof(title), typeof(created_at), typeof(updated_at)
			FROM conversations WHERE id = ?`, record.ID).Scan(&title, &created, &updated, &titleType, &createdType, &updatedType); err != nil {
			t.Fatal(err)
		}
		if title != record.Title || created != record.CreatedAt.UTC().Format(time.RFC3339Nano) || updated != record.UpdatedAt.UTC().Format(time.RFC3339Nano) ||
			titleType != "text" || createdType != "text" || updatedType != "text" {
			t.Fatalf("stored format changed: title=%q created=%q updated=%q types=%s/%s/%s", title, created, updated, titleType, createdType, updatedType)
		}
		read, err := repo.GetByID(ctx, record.ID)
		if err != nil || read.Title != record.Title || !read.CreatedAt.Equal(record.CreatedAt) || !read.UpdatedAt.Equal(record.UpdatedAt) {
			t.Fatalf("roundtrip: record=%+v err=%v", read, err)
		}
	}
	// Empty title and caller-supplied zero time must overwrite nonzero values.
	if err := repo.UpdateTitle(ctx, "roundtrip", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	var title, created, updated string
	if err := db.QueryRowContext(ctx, `SELECT title, created_at, updated_at FROM conversations WHERE id = 'roundtrip'`).Scan(&title, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if title != "" || created != createdAt.UTC().Format(time.RFC3339Nano) || updated != "0001-01-01T00:00:00Z" {
		t.Fatalf("zero update changed: title=%q created=%q updated=%q", title, created, updated)
	}
	if err := repo.UpdateTitle(ctx, "roundtrip", "纳秒时间", updatedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM conversations WHERE id = 'roundtrip'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if updated != "2024-02-02T20:06:07.987654321Z" {
		t.Fatalf("update did not preserve UTC nanoseconds: %q", updated)
	}
	if err := repo.Create(ctx, conversation.Conversation{ID: "legacy", Title: "replacement"}); err == nil {
		t.Fatal("duplicate Create unexpectedly succeeded")
	}
	unchanged, err := repo.GetByID(ctx, "legacy")
	if err != nil || !reflect.DeepEqual(unchanged, got) {
		t.Fatalf("duplicate Create changed the original: %+v, %v", unchanged, err)
	}
}

func TestMessageORMSQLFixtureFieldsAndEmptyFormat(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	conversationORMFixture(t, ctx, db, "c1")
	messageORMFixture(t, ctx, db, "empty", "c1", 2)
	const createdText = "2024-02-03T04:05:06.123456789+08:00"
	const updatedText = "2024-02-03T04:06:07.987654321Z"
	conversationORMExec(t, ctx, db, `INSERT INTO messages
		(id, conversation_id, client_message_id, sequence, role, content, status, error_code, error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"failed", "c1", "client-1", 1, "assistant", "部分输出\n内容", "failed", "model_error", "模型失败", createdText, updatedText)
	repo := sqlite.NewMessageRepository(db)
	got, err := repo.GetByID(ctx, "failed")
	if err != nil {
		t.Fatal(err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdText)
	if err != nil {
		t.Fatal(err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedText)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "failed" || got.ConversationID != "c1" || got.ClientMessageID != "client-1" || got.Sequence != 1 ||
		got.Role != conversation.MessageRoleAssistant || got.Content != "部分输出\n内容" || got.Status != conversation.MessageStatusFailed ||
		got.ErrorCode != "model_error" || got.ErrorMessage != "模型失败" || !got.CreatedAt.Equal(createdAt) || !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("message SQL fixture changed: %+v", got)
	}
	empty, err := repo.GetByID(ctx, "empty")
	if err != nil || empty.ClientMessageID != "" || empty.Content != "" || empty.ErrorCode != "" || empty.ErrorMessage != "" {
		t.Fatalf("historical empty fields changed: %+v, %v", empty, err)
	}
	var client, content, code, message, createdType, updatedType string
	if err := db.QueryRowContext(ctx, `SELECT typeof(client_message_id), typeof(content), typeof(error_code), typeof(error_message), typeof(created_at), typeof(updated_at)
		FROM messages WHERE id = 'empty'`).Scan(&client, &content, &code, &message, &createdType, &updatedType); err != nil {
		t.Fatal(err)
	}
	if client != "text" || content != "text" || code != "text" || message != "text" || createdType != "text" || updatedType != "text" {
		t.Fatalf("empty fields must remain TEXT, not NULL: %s/%s/%s/%s/%s/%s", client, content, code, message, createdType, updatedType)
	}
	// These columns were never nullable; the original migrations still enforce that boundary.
	for _, query := range []string{
		`UPDATE conversations SET title = NULL WHERE id = 'c1'`,
		`UPDATE conversations SET created_at = NULL WHERE id = 'c1'`,
		`UPDATE messages SET client_message_id = NULL WHERE id = 'empty'`,
		`UPDATE messages SET content = NULL WHERE id = 'empty'`,
		`UPDATE messages SET error_code = NULL WHERE id = 'empty'`,
		`UPDATE messages SET error_message = NULL WHERE id = 'empty'`,
		`UPDATE messages SET updated_at = NULL WHERE id = 'empty'`,
	} {
		if _, err := db.ExecContext(ctx, query); err == nil {
			t.Fatalf("NULL unexpectedly accepted: %s", query)
		}
	}
}

func TestConversationORMMissingAndEmptyLists(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	conversations := sqlite.NewConversationRepository(db)
	messages := sqlite.NewMessageRepository(db)
	if items, err := conversations.List(ctx); err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty conversations must be a nonnil slice: %+v, %v", items, err)
	}
	if items, err := messages.ListByConversation(ctx, "missing"); err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty messages must be a nonnil slice: %+v, %v", items, err)
	}
	conversationORMFixture(t, ctx, db, "existing")
	before, err := conversations.GetByID(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := conversations.GetByID(ctx, "missing"); !errors.Is(err, domainerr.ErrNotFound) || got != (conversation.Conversation{}) {
		t.Fatalf("missing conversation: %+v, %v", got, err)
	}
	if got, err := messages.GetByID(ctx, "missing"); !errors.Is(err, domainerr.ErrNotFound) || got != (conversation.Message{}) {
		t.Fatalf("missing message: %+v, %v", got, err)
	}
	for _, err := range []error{
		conversations.UpdateTitle(ctx, "missing", "", time.Time{}),
		conversations.Delete(ctx, "missing"),
		messages.Delete(ctx, "missing"),
	} {
		if !errors.Is(err, domainerr.ErrNotFound) {
			t.Fatalf("missing mutation should return ErrNotFound: %v", err)
		}
	}
	items, err := conversations.List(ctx)
	if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0], before) {
		t.Fatalf("missing mutation inserted or touched a conversation: %+v, %v", items, err)
	}
}

func TestConversationORMListOrdering(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	for _, id := range []string{"a", "newest", "z"} {
		conversationORMFixture(t, ctx, db, id)
	}
	conversationORMExec(t, ctx, db, `UPDATE conversations SET updated_at = '2021-01-02T03:04:05.123456789Z' WHERE id = 'newest'`)
	conversations, err := sqlite.NewConversationRepository(db).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, record := range conversations {
		ids = append(ids, record.ID)
	}
	if !reflect.DeepEqual(ids, []string{"newest", "z", "a"}) {
		t.Fatalf("conversation order changed: %v", ids)
	}
	messageORMFixture(t, ctx, db, "a-last", "a", 3)
	messageORMFixture(t, ctx, db, "z-first", "a", 1)
	messageORMFixture(t, ctx, db, "middle", "a", 2)
	messageORMFixture(t, ctx, db, "other", "z", 1)
	messages, err := sqlite.NewMessageRepository(db).ListByConversation(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	ids = nil
	for _, record := range messages {
		ids = append(ids, record.ID)
	}
	if !reflect.DeepEqual(ids, []string{"z-first", "middle", "a-last"}) {
		t.Fatalf("message order or conversation filter changed: %v", ids)
	}
}

func TestConversationORMRejectsMalformedTimestamps(t *testing.T) {
	for _, table := range []string{"conversations", "messages"} {
		for _, column := range []string{"created_at", "updated_at"} {
			t.Run(table+"/"+column, func(t *testing.T) {
				db, ctx := newSQLTestDB(t)
				conversationORMFixture(t, ctx, db, "c1")
				messageORMFixture(t, ctx, db, "m1", "c1", 1)
				// Only fixed table and column names from this test enter the SQL fragment.
				conversationORMExec(t, ctx, db, "UPDATE "+table+" SET "+column+" = 'not-a-time'")
				var getErr, listErr error
				if table == "conversations" {
					repo := sqlite.NewConversationRepository(db)
					got, err := repo.GetByID(ctx, "c1")
					getErr = err
					if got != (conversation.Conversation{}) {
						t.Fatalf("malformed timestamp returned a record: %+v", got)
					}
					items, err := repo.List(ctx)
					listErr = err
					if items != nil {
						t.Fatalf("malformed timestamp returned a list: %+v", items)
					}
				} else {
					repo := sqlite.NewMessageRepository(db)
					got, err := repo.GetByID(ctx, "m1")
					getErr = err
					if got != (conversation.Message{}) {
						t.Fatalf("malformed timestamp returned a record: %+v", got)
					}
					items, err := repo.ListByConversation(ctx, "c1")
					listErr = err
					if items != nil {
						t.Fatalf("malformed timestamp returned a list: %+v", items)
					}
				}
				for _, err := range []error{getErr, listErr} {
					var parseErr *time.ParseError
					if !errors.As(err, &parseErr) {
						t.Fatalf("expected timestamp parse error, got %v", err)
					}
				}
			})
		}
	}
}

func TestConversationORMDeleteCascadesSQLFixture(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	for _, id := range []string{"delete", "keep"} {
		conversationORMFixture(t, ctx, db, id)
		messageORMFixture(t, ctx, db, "message-"+id, id, 1)
		conversationORMExec(t, ctx, db, `INSERT INTO generation_events (assistant_message_id, sequence, kind, text, created_at)
			VALUES (?, 1, 'text', '', '2020-01-02T03:04:05Z')`, "message-"+id)
	}
	if err := sqlite.NewConversationRepository(db).Delete(ctx, "delete"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM conversations WHERE id = 'delete'`,
		`SELECT count(*) FROM messages WHERE conversation_id = 'delete'`,
		`SELECT count(*) FROM generation_events WHERE assistant_message_id = 'message-delete'`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade left records: count=%d err=%v query=%s", count, err, query)
		}
	}
	if got, err := sqlite.NewMessageRepository(db).GetByID(ctx, "message-keep"); err != nil || got.ConversationID != "keep" {
		t.Fatalf("cascade affected another conversation: %+v, %v", got, err)
	}
	if got, err := sqlite.NewMessageRepository(db).ListByConversation(ctx, "delete"); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("deleted conversation messages: %+v, %v", got, err)
	}
}

func TestMessageORMDeleteTouchesConversationAndRollsBack(t *testing.T) {
	for _, failure := range []string{"none", "delete", "touch", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, ctx := newSQLTestDB(t)
			conversationORMFixture(t, ctx, db, "c1")
			conversationORMFixture(t, ctx, db, "other")
			messageORMFixture(t, ctx, db, "delete", "c1", 1)
			messageORMFixture(t, ctx, db, "keep", "c1", 2)
			conversationORMExec(t, ctx, db, `INSERT INTO generation_events (assistant_message_id, sequence, kind, text, created_at)
				VALUES ('delete', 1, 'text', 'output', '2020-01-02T03:04:05Z')`)
			conversations := sqlite.NewConversationRepository(db)
			before, err := conversations.GetByID(ctx, "c1")
			if err != nil {
				t.Fatal(err)
			}
			otherBefore, err := conversations.GetByID(ctx, "other")
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "delete":
				conversationORMExec(t, ctx, db, `CREATE TRIGGER reject_message_delete BEFORE DELETE ON messages
					BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`)
			case "touch":
				conversationORMExec(t, ctx, db, `CREATE TRIGGER reject_conversation_touch BEFORE UPDATE OF updated_at ON conversations
					BEGIN SELECT RAISE(ABORT, 'injected touch failure'); END`)
			case "commit":
				conversationORMExec(t, ctx, db, `CREATE TABLE deferred_touch_failure (
					conversation_id TEXT REFERENCES conversations(id) DEFERRABLE INITIALLY DEFERRED)`)
				conversationORMExec(t, ctx, db, `CREATE TRIGGER defer_conversation_touch AFTER UPDATE OF updated_at ON conversations
					BEGIN INSERT INTO deferred_touch_failure VALUES ('missing'); END`)
			}
			start := time.Now().UTC()
			err = sqlite.NewMessageRepository(db).Delete(ctx, "delete")
			end := time.Now().UTC()
			if (failure == "none" && err != nil) || (failure != "none" && err == nil) {
				t.Fatalf("unexpected delete result: %v", err)
			}
			after, err := conversations.GetByID(ctx, "c1")
			if err != nil {
				t.Fatal(err)
			}
			var messageCount, eventCount int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE id = 'delete'`).Scan(&messageCount); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM generation_events WHERE assistant_message_id = 'delete'`).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			if failure == "none" {
				if messageCount != 0 || eventCount != 0 || after.UpdatedAt.Before(start) || after.UpdatedAt.After(end) || !after.CreatedAt.Equal(before.CreatedAt) || after.Title != before.Title {
					t.Fatalf("delete/touch changed: messages=%d events=%d before=%+v after=%+v", messageCount, eventCount, before, after)
				}
			} else if messageCount != 1 || eventCount != 1 || !reflect.DeepEqual(after, before) {
				t.Fatalf("failed delete did not roll back: messages=%d events=%d before=%+v after=%+v", messageCount, eventCount, before, after)
			}
			if failure == "commit" {
				var count int
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM deferred_touch_failure`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("commit failure left deferred writes: count=%d err=%v", count, err)
				}
			}
			otherAfter, err := conversations.GetByID(ctx, "other")
			if err != nil || !reflect.DeepEqual(otherAfter, otherBefore) {
				t.Fatalf("delete touched another conversation: %+v, %v", otherAfter, err)
			}
			if _, err := sqlite.NewMessageRepository(db).GetByID(ctx, "keep"); err != nil {
				t.Fatalf("delete affected sibling: %v", err)
			}
		})
	}
}
