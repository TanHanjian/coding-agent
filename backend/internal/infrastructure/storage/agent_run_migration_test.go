package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestAgentRunSchemaConstraints(t *testing.T) {
	for _, status := range []string{"pending", "running", "waiting_tool", "completed", "failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			db := testDB(t)
			finishedAt, finalEntryID, errorCode := "", "", ""
			if status == "completed" || status == "failed" || status == "cancelled" {
				finishedAt = "done"
			}
			if status == "completed" {
				finalEntryID = "e-r1"
			}
			if status == "failed" {
				errorCode = "model_failed"
			}
			seedRunFixture(t, db, "c1", "r1", "client-r1", status, finishedAt, finalEntryID, errorCode)

			cases := []struct{ name, assignment string }{
				{"unknown status", "status = 'invalid'"},
				{"empty client key", "client_message_id = ''"},
				{"negative base version", "base_head_version = -1"},
				{"negative head version", "head_version = -1"},
			}
			if finishedAt == "" {
				cases = append(cases, struct{ name, assignment string }{"active with finished_at", "finished_at = 'done'"})
			} else {
				cases = append(cases, struct{ name, assignment string }{"terminal without finished_at", "finished_at = NULL"})
			}
			if status == "completed" {
				cases = append(cases,
					struct{ name, assignment string }{"completed without final", "final_entry_id = NULL"},
					struct{ name, assignment string }{"completed with error", "error_code = 'model_failed'"})
			} else {
				cases = append(cases, struct{ name, assignment string }{"noncompleted with final", "final_entry_id = 'e-r1'"})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					_, err := db.Exec(`UPDATE agent_runs SET ` + tc.assignment + ` WHERE id = 'r1'`)
					assertAgentRunSQLiteConstraint(t, err, 275) // SQLITE_CONSTRAINT_CHECK
				})
			}
		})
	}
}

func TestAgentRunClientAndMessageUniqueness(t *testing.T) {
	db := testDB(t)
	seedRunFixture(t, db, "c1", "r1", "same-client", "failed", "done", "", "model_failed")
	seedMessagesAndEntries(t, db, "c1", "extra")
	for _, tc := range []struct{ name, clientID, userID, assistantID string }{
		{"client key", "same-client", "u-extra", "a-extra"},
		{"user message", "new-client", "u-r1", "a-extra"},
		{"assistant message", "new-client", "u-extra", "a-r1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`INSERT INTO agent_runs (
				id, conversation_id, client_message_id, user_message_id, assistant_message_id,
				base_head_version, user_entry_id, last_entry_id, last_closed_entry_id,
				head_version, status, created_at, finished_at
			) VALUES ('duplicate', 'c1', ?, ?, ?, 0, 'e-extra', 'e-extra', 'e-extra', 0, 'failed', 'now', 'done')`, tc.clientID, tc.userID, tc.assistantID)
			assertAgentRunSQLiteConstraint(t, err, 2067) // SQLITE_CONSTRAINT_UNIQUE
		})
	}
	insertRun(t, db, "r2", "c1", "new-client", "u-extra", "a-extra", "e-extra", "failed", "done", "", "")
	seedRunFixture(t, db, "c2", "r3", "same-client", "failed", "done", "", "model_failed")
}

func TestAgentRunOneActive(t *testing.T) {
	for _, active := range []string{"pending", "running", "waiting_tool"} {
		for _, terminal := range []string{"completed", "failed", "cancelled"} {
			t.Run(active+" to "+terminal, func(t *testing.T) {
				db := testDB(t)
				seedRunFixture(t, db, "c1", "r1", "client-r1", active, "", "", "")
				seedRunFixture(t, db, "c2", "other", "client-other", active, "", "", "")
				seedMessagesAndEntries(t, db, "c1", "extra")
				for _, candidate := range []string{"pending", "running", "waiting_tool"} {
					_, err := db.Exec(`INSERT INTO agent_runs (
						id, conversation_id, client_message_id, user_message_id, assistant_message_id,
						base_head_version, user_entry_id, last_entry_id, last_closed_entry_id,
						head_version, status, created_at
					) VALUES ('second', 'c1', 'client-second', 'u-extra', 'a-extra', 0, 'e-extra', 'e-extra', 'e-extra', 0, ?, 'now')`, candidate)
					assertAgentRunSQLiteConstraint(t, err, 2067) // SQLITE_CONSTRAINT_UNIQUE
				}
				var finalEntryID any
				if terminal == "completed" {
					finalEntryID = "e-r1"
				}
				if _, err := db.Exec(`UPDATE agent_runs SET status = ?, final_entry_id = ?, finished_at = 'done' WHERE id = 'r1'`, terminal, finalEntryID); err != nil {
					t.Fatalf("terminal transition: %v", err)
				}
				insertRun(t, db, "second", "c1", "client-second", "u-extra", "a-extra", "e-extra", active, "", "", "")
			})
		}
	}
}

func TestAgentRunReferencesAndDeleteBehavior(t *testing.T) {
	db := testDB(t)
	seedRunFixture(t, db, "c1", "r1", "client-r1", "running", "", "", "")
	seedRunFixture(t, db, "c2", "r2", "client-r2", "pending", "", "", "")
	seedMessagesAndEntries(t, db, "c1", "extra-c1")
	seedMessagesAndEntries(t, db, "c2", "extra-c2")

	assertDeferredInsertFails := func(name, runID, conversationID, userMessageID, assistantMessageID, baseEntryID, userEntryID, lastEntryID, lastClosedEntryID, finalEntryID, status, finishedAt string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			_, err = tx.Exec(`INSERT INTO agent_runs (
				id, conversation_id, client_message_id, user_message_id, assistant_message_id,
				base_entry_id, base_head_version, user_entry_id, last_entry_id, last_closed_entry_id,
				final_entry_id, head_version, status, created_at, finished_at
			) VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, 0, ?, 'now', ?)`, runID, conversationID, runID, userMessageID, assistantMessageID, nullable(baseEntryID), userEntryID, lastEntryID, lastClosedEntryID, nullable(finalEntryID), status, nullable(finishedAt))
			if err != nil {
				t.Fatalf("deferred insert should reach commit: %v", err)
			}
			assertAgentRunSQLiteConstraint(t, tx.Commit(), 787) // SQLITE_CONSTRAINT_FOREIGNKEY
		})
	}
	assertDeferredInsertFails("user message from another conversation", "cross-user-message", "c1", "u-extra-c2", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("assistant message from another conversation", "cross-assistant-message", "c1", "u-extra-c1", "a-extra-c2", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("missing user message", "missing-user-message", "c1", "missing", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("missing assistant message", "missing-assistant-message", "c1", "u-extra-c1", "missing", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("base entry from another conversation", "cross-base-entry", "c1", "u-extra-c1", "a-extra-c1", "e-extra-c2", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("user entry from another conversation", "cross-user-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c2", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("last entry from another conversation", "cross-last-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "e-extra-c2", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("last closed entry from another conversation", "cross-closed-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "e-extra-c2", "", "failed", "done")
	assertDeferredInsertFails("final entry from another conversation", "cross-final-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "e-extra-c2", "completed", "done")
	assertDeferredInsertFails("missing base entry", "missing-base-entry", "c1", "u-extra-c1", "a-extra-c1", "absent", "e-extra-c1", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("missing user entry", "missing-user-entry", "c1", "u-extra-c1", "a-extra-c1", "", "absent", "e-extra-c1", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("missing last entry", "missing-last-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "absent", "e-extra-c1", "", "failed", "done")
	assertDeferredInsertFails("missing last closed entry", "missing-closed-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "absent", "", "failed", "done")
	assertDeferredInsertFails("missing final entry", "missing-final-entry", "c1", "u-extra-c1", "a-extra-c1", "", "e-extra-c1", "e-extra-c1", "e-extra-c1", "absent", "completed", "done")

	if _, err := db.Exec(`INSERT INTO agent_runs (
		id, conversation_id, client_message_id, user_message_id, assistant_message_id,
		base_entry_id, base_head_version, user_entry_id, last_entry_id, last_closed_entry_id,
		final_entry_id, head_version, status, created_at, finished_at
	) VALUES ('same-owner', 'c1', 'client-same-owner', 'u-extra-c1', 'a-extra-c1',
		'e-extra-c1', 0, 'e-extra-c1', 'e-extra-c1', 'e-extra-c1', 'e-extra-c1', 0, 'completed', 'now', 'done')`); err != nil {
		t.Fatalf("all same-owner references should be accepted: %v", err)
	}

	for _, messageID := range []string{"u-r1", "a-r1"} {
		t.Run("delete "+messageID, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, messageID); err != nil {
				t.Fatalf("deferred message delete should reach commit: %v", err)
			}
			assertAgentRunSQLiteConstraint(t, tx.Commit(), 787)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE id = ?`, messageID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("failed delete must preserve the referenced message")
			}
		})
	}

	if _, err := db.Exec(`DELETE FROM conversations WHERE id = 'c1'`); err != nil {
		t.Fatalf("conversation cascade: %v", err)
	}
	for _, table := range []string{"agent_runs", "messages", "conversation_entry_nodes"} {
		var remaining int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE conversation_id = 'c1'`).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatalf("expected conversation cascade to remove %s, got %d", table, remaining)
		}
	}
	var otherRuns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE conversation_id = 'c2'`).Scan(&otherRuns); err != nil {
		t.Fatal(err)
	}
	if otherRuns != 1 {
		t.Fatalf("cascade affected another conversation: runs=%d", otherRuns)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign_key_check reported a violation after cascade")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateFromVersionEightWithLegacyMessages(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "upgrade", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL, checksum TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 8; version++ {
		var filename string
		for _, entry := range entries {
			var found int
			if _, err := fmt.Sscanf(entry.Name(), "%d_", &found); err == nil && found == version {
				filename = entry.Name()
				break
			}
		}
		if filename == "" {
			t.Fatalf("missing migration %d", version)
		}
		sqlBytes, err := migrationFiles.ReadFile("migrations/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
		sum := sha256.Sum256(sqlBytes)
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at, checksum) VALUES (?, ?, ?)`, version, time.Now().UTC().Format(time.RFC3339Nano), hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO conversations(id, title, created_at, updated_at) VALUES ('legacy', '', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages(id, conversation_id, sequence, role, status, created_at, updated_at) VALUES ('legacy-user', 'legacy', 1, 'user', 'completed', 'now', 'now'), ('legacy-assistant', 'legacy', 2, 'assistant', 'completed', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("upgrade v8 to current: %v", err)
	}
	var legacyMessageCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id = 'legacy'
		AND ((id = 'legacy-user' AND sequence = 1 AND role = 'user')
		OR (id = 'legacy-assistant' AND sequence = 2 AND role = 'assistant'))
		AND status = 'completed' AND created_at = 'now' AND updated_at = 'now'`).Scan(&legacyMessageCount); err != nil {
		t.Fatal(err)
	}
	if legacyMessageCount != 2 {
		t.Fatalf("upgrade must preserve legacy messages, got %d", legacyMessageCount)
	}
	var runCount, entryCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_entry_nodes`).Scan(&entryCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 0 || entryCount != 0 {
		t.Fatalf("legacy data should not be backfilled: runs=%d entries=%d", runCount, entryCount)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	var migrationCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 10 {
		t.Fatalf("expected 10 recorded migrations, got %d", migrationCount)
	}
}

func seedRunFixture(t *testing.T, db *DB, conversationID, runID, clientID, status, finishedAt, finalEntryID, errorCode string) {
	t.Helper()
	if _, err := db.Exec(`INSERT OR IGNORE INTO conversations(id, title, created_at, updated_at) VALUES (?, '', 'now', 'now')`, conversationID); err != nil {
		t.Fatal(err)
	}
	seedMessagesAndEntries(t, db, conversationID, runID)
	insertRun(t, db, runID, conversationID, clientID, "u-"+runID, "a-"+runID, "e-"+runID, status, finishedAt, finalEntryID, errorCode)
}

func seedMessagesAndEntries(t *testing.T, db *DB, conversationID, suffix string) {
	t.Helper()
	var sequence, appendIndex int
	if err := db.QueryRow(`SELECT COALESCE(MAX(sequence), 0) + 1 FROM messages WHERE conversation_id = ?`, conversationID).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages(id, conversation_id, sequence, role, status, created_at, updated_at) VALUES (?, ?, ?, 'user', 'completed', 'now', 'now'), (?, ?, ?, 'assistant', 'completed', 'now', 'now')`, "u-"+suffix, conversationID, sequence, "a-"+suffix, conversationID, sequence+1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(MAX(append_index), 0) + 1 FROM conversation_entry_nodes WHERE conversation_id = ?`, conversationID).Scan(&appendIndex); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversation_entry_nodes(id, conversation_id, kind, payload_version, payload_json, append_index, depth, created_at) VALUES (?, ?, 'message', 1, '{}', ?, 0, 'now')`, "e-"+suffix, conversationID, appendIndex); err != nil {
		t.Fatal(err)
	}
}

func insertRun(t *testing.T, db *DB, runID, conversationID, clientID, userMessageID, assistantMessageID, entryID, status, finishedAt, finalEntryID, errorCode string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO agent_runs (
		id, conversation_id, client_message_id, user_message_id, assistant_message_id,
		base_head_version, user_entry_id, last_entry_id, last_closed_entry_id,
		final_entry_id, head_version, status, error_code, created_at, finished_at
	) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, 0, ?, ?, 'now', ?)`, runID, conversationID, clientID, userMessageID, assistantMessageID, entryID, entryID, entryID, nullable(finalEntryID), status, errorCode, nullable(finishedAt))
	if err != nil {
		t.Fatalf("insert run %s: %v", runID, err)
	}
}

func assertAgentRunSQLiteConstraint(t *testing.T, err error, code int) {
	t.Helper()
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code() != code {
		t.Fatalf("expected SQLite constraint code %d, got %v", code, err)
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
