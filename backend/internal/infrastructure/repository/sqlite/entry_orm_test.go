package sqlite

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// The fixture is written independently of ORM models to catch storage-format drift.
func TestEntryORMReadsLegacyTextAndWritesCompatibleRows(t *testing.T) {
	db, ctx, repo := newEntryTxFixture(t)
	created := "2026-05-06T07:08:09.123456789+08:00"
	payload := `{"role":"user","content":[{"type":"text","text":"historical text"}]}`
	if _, err := db.ExecContext(ctx, `INSERT INTO conversation_entry_nodes
		(id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at)
		VALUES ('historical-entry', 'entry-tx', NULL, 'message', 1, ?, 1, 0, ?)`, payload, created); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO conversation_entry_heads
		(conversation_id, active_leaf_id, version, updated_at) VALUES ('entry-tx', 'historical-entry', 1, ?)`, created); err != nil {
		t.Fatal(err)
	}
	path, err := repo.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", UseActiveLeaf: true})
	if err != nil || len(path.Entries) != 1 {
		t.Fatalf("legacy path = %+v, %v", path, err)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, created)
	if path.Entries[0].ParentID != nil || !path.Entries[0].CreatedAt.Equal(wantTime) || path.Entries[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("legacy mapping = %+v", path.Entries[0])
	}
	appended, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "entry-tx", ExpectedHead: entryTxToken(path.Head), Message: entryTxMessage(conversation.EntryRoleAssistant),
	})
	if err != nil {
		t.Fatal(err)
	}
	var storageType, rawPayload, parent, rawCreated string
	var payloadVersion int
	if err := db.QueryRowContext(ctx, `SELECT typeof(payload_json), payload_json, parent_id, created_at, payload_version
		FROM conversation_entry_nodes WHERE id = ?`, appended.Entry.ID).Scan(&storageType, &rawPayload, &parent, &rawCreated, &payloadVersion); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEntryMessage(payloadVersion, []byte(rawPayload))
	if storageType != "text" || parent != "historical-entry" || rawCreated != formatTime(appended.Entry.CreatedAt) || err != nil || !reflect.DeepEqual(decoded, appended.Entry.Message) {
		t.Fatalf("new row = %s, %s, %s, %s, %+v, %v", storageType, rawPayload, parent, rawCreated, decoded, err)
	}
}

func TestEntryORMRejectsMissingAndCorruptRows(t *testing.T) {
	for _, corruption := range []string{"missing", "timestamp", "payload_version", "payload"} {
		t.Run(corruption, func(t *testing.T) {
			db, ctx, repo := newEntryTxFixture(t)
			if corruption == "missing" {
				_, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "entry-tx", ID: "absent"})
				if !errors.Is(err, domainerr.ErrNotFound) {
					t.Fatalf("missing row = %v", err)
				}
				leaf := conversation.EntryID("absent")
				path, err := repo.ReadPath(ctx, conversation.PathQuery{ConversationID: "entry-tx", LeafID: &leaf})
				if !errors.Is(err, conversation.ErrEntryCorrupt) || !reflect.DeepEqual(path, conversation.EntryPath{}) {
					t.Fatalf("missing CTE target = %+v, %v", path, err)
				}
				return
			}
			at, version, payload := formatTime(time.Now()), 1, `{"role":"user","content":[{"type":"text","text":"legacy"}]}`
			switch corruption {
			case "timestamp":
				at = "invalid timestamp"
			case "payload_version":
				version = 2
				// Model a damaged database independently of the schema write guard.
				if _, err := db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
					t.Fatal(err)
				}
			case "payload":
				payload = `{}`
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO conversation_entry_nodes
				(id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at)
				VALUES ('bad-entry', 'entry-tx', NULL, 'message', ?, ?, 1, 0, ?)`, version, payload, at); err != nil {
				t.Fatal(err)
			}
			entry, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "entry-tx", ID: "bad-entry"})
			wantError := conversation.ErrEntryCorrupt
			if corruption == "payload_version" {
				wantError = conversation.ErrEntryUnsupportedVersion
			}
			if !errors.Is(err, wantError) || !reflect.DeepEqual(entry, conversation.ConversationEntry{}) {
				t.Fatalf("corrupt row = %+v, %v", entry, err)
			}
		})
	}
}
