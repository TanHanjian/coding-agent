package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func newRegressionEntryTree(t *testing.T) (*storage.DB, context.Context, *sqlite.EntryTreeRepository) {
	t.Helper()
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "regression")
	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return db, ctx, repo
}

func regressionHeadToken(head conversation.EntryHead) conversation.HeadToken {
	return conversation.HeadToken{ActiveLeafID: head.ActiveLeafID, Version: head.Version}
}

func appendRegressionMessage(t *testing.T, ctx context.Context, repo *sqlite.EntryTreeRepository, head conversation.EntryHead, msg conversation.EntryMessage) conversation.AppendEntryResult {
	t.Helper()
	res, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "regression",
		ExpectedHead:   regressionHeadToken(head),
		Message:        msg,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func regressionUserMessage() conversation.EntryMessage {
	return conversation.EntryMessage{
		Role:    conversation.EntryRoleUser,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("user")}},
	}
}

func registerRegressionToolPolicy(t *testing.T) {
	t.Helper()
	conversation.ClearToolPolicies()
	t.Cleanup(conversation.ClearToolPolicies)
	if err := conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName:        "regression_search",
		Version:         "v1",
		ArgumentFields:  []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 100}},
		ResultFields:    []conversation.ProjectionFieldRule{{SourceKey: "summary", StoredKey: "summary", MaxBytes: 100}},
		MaxPreviewBytes: 100,
	}); err != nil {
		t.Fatal(err)
	}
}

func regressionToolMessage(t *testing.T) conversation.EntryMessage {
	t.Helper()
	call := mustProjectCall(t, "regression_search", "v1", "call-1", `{"q":"approved"}`)
	return conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopTools,
		Content:    []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}},
	}
}

func regressionToolResult(t *testing.T, callEntryID conversation.EntryID) conversation.EntryMessage {
	t.Helper()
	return conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: callEntryID,
			CallID:      "call-1",
			ToolName:    "regression_search",
			Result:      mustProjectResult(t, "regression_search", "v1", `{"summary":"approved"}`),
		},
	}
}

func assertRegressionTreeState(t *testing.T, ctx context.Context, db *storage.DB, repo *sqlite.EntryTreeRepository, expected conversation.EntryHead, expectedNodes int) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_entry_nodes WHERE conversation_id = ?", "regression").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expectedNodes {
		t.Fatalf("node count = %d, want %d", count, expectedNodes)
	}
	head, err := repo.GetHead(ctx, "regression")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(head, expected) {
		t.Fatalf("head changed: got %+v, want %+v", head, expected)
	}
}

func TestEntryTreeCommitFailureReturnsZeroAndRollsBack(t *testing.T) {
	for _, operation := range []string{"append", "move"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, repo := newRegressionEntryTree(t)
			root := appendRegressionMessage(t, ctx, repo, conversation.EntryHead{}, regressionUserMessage())
			// The deferred FK fails at COMMIT, after the transaction callback has
			// prepared its result. The repository must not return that result.
			if _, err := db.ExecContext(ctx, `
				CREATE TRIGGER inject_entry_head_commit_failure
				AFTER UPDATE ON conversation_entry_heads
				WHEN NEW.active_leaf_id IS NOT 'e_missing'
				BEGIN
					UPDATE conversation_entry_heads SET active_leaf_id = 'e_missing'
					WHERE conversation_id = NEW.conversation_id;
				END;
			`); err != nil {
				t.Fatal(err)
			}

			if operation == "append" {
				result, err := repo.Append(ctx, conversation.AppendEntryInput{
					ConversationID: "regression",
					ExpectedHead:   regressionHeadToken(root.Head),
					Message:        regressionUserMessage(),
				})
				if err == nil {
					t.Fatal("expected COMMIT failure")
				}
				if !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
					t.Fatalf("uncommitted append result returned: %+v", result)
				}
			} else {
				result, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
					ConversationID: "regression",
					ExpectedHead:   regressionHeadToken(root.Head),
					TargetID:       nil,
				})
				if err == nil {
					t.Fatal("expected COMMIT failure")
				}
				if !reflect.DeepEqual(result, conversation.EntryHead{}) {
					t.Fatalf("uncommitted move result returned: %+v", result)
				}
			}

			assertRegressionTreeState(t, ctx, db, repo, root.Head, 1)
			if _, err := db.ExecContext(ctx, "DROP TRIGGER inject_entry_head_commit_failure"); err != nil {
				t.Fatal(err)
			}
			appendRegressionMessage(t, ctx, repo, root.Head, regressionUserMessage())
		})
	}
}

func TestEntryTreeGetIsScopedAndReturnsNotFound(t *testing.T) {
	db, ctx, repo := newRegressionEntryTree(t)
	root := appendRegressionMessage(t, ctx, repo, conversation.EntryHead{}, regressionUserMessage())
	createSQLConversation(t, ctx, db, "other")
	for _, ref := range []conversation.EntryRef{
		{ConversationID: "missing", ID: root.Entry.ID},
		{ConversationID: "other", ID: root.Entry.ID},
		{ConversationID: "regression", ID: "e_missing"},
	} {
		entry, err := repo.Get(ctx, ref)
		if !errors.Is(err, domainerr.ErrNotFound) || !reflect.DeepEqual(entry, conversation.ConversationEntry{}) {
			t.Fatalf("Get(%+v) = %+v, %v; want zero entry and NotFound", ref, entry, err)
		}
	}
	entry, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "regression", ID: root.Entry.ID})
	if err != nil || !reflect.DeepEqual(entry, root.Entry) {
		t.Fatalf("Get existing entry = %+v, %v", entry, err)
	}
}

func TestEntryTreeInsertConflictsCannotRewriteEntries(t *testing.T) {
	for _, variant := range []string{"same_id_replace", "same_index_replace", "upsert_update"} {
		t.Run(variant, func(t *testing.T) {
			db, ctx, repo := newRegressionEntryTree(t)
			root := appendRegressionMessage(t, ctx, repo, conversation.EntryHead{}, regressionUserMessage())
			id := string(root.Entry.ID)
			if variant == "same_index_replace" {
				id = "e_replacement"
			}
			verb, suffix := "INSERT OR REPLACE", ""
			if variant == "upsert_update" {
				verb = "INSERT"
				suffix = " ON CONFLICT(id) DO UPDATE SET payload_json = excluded.payload_json"
			}
			query := verb + ` INTO conversation_entry_nodes
				(id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at)
				VALUES (?, 'regression', NULL, 'message', 1, ?, 1, 0, ?)` + suffix
			if _, err := db.ExecContext(ctx, query, id,
				`{"role":"user","content":[{"type":"text","text":"rewritten"}]}`,
				time.Now().UTC().Format(time.RFC3339Nano)); err == nil {
				t.Fatal("conflicting insert unexpectedly succeeded")
			}
			assertRegressionTreeState(t, ctx, db, repo, root.Head, 1)
			got, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "regression", ID: root.Entry.ID})
			if err != nil || !reflect.DeepEqual(got, root.Entry) {
				t.Fatalf("original entry was changed: %+v, %v", got, err)
			}
		})
	}
}

func TestEntryTreeRejectsForgedToolPayloadBeforePersistence(t *testing.T) {
	for _, variant := range []string{"unregistered", "empty_policy", "sensitive_call_field", "sensitive_result_field", "raw_result_preview"} {
		t.Run(variant, func(t *testing.T) {
			registerRegressionToolPolicy(t)
			db, ctx, repo := newRegressionEntryTree(t)
			root := appendRegressionMessage(t, ctx, repo, conversation.EntryHead{}, regressionUserMessage())
			currentHead, nodes := root.Head, 1
			var msg conversation.EntryMessage
			if variant == "sensitive_result_field" || variant == "raw_result_preview" {
				assistant := appendRegressionMessage(t, ctx, repo, currentHead, regressionToolMessage(t))
				currentHead, nodes = assistant.Head, 2
				msg = regressionToolResult(t, assistant.Entry.ID)
				payload := `{"policyVersion":"v1","fields":[{"name":"Authorization","value":"test-only-secret"}],"preview":"","truncated":false}`
				if variant == "raw_result_preview" {
					payload = `{"policyVersion":"v1","fields":[{"name":"summary","value":"approved"}],"preview":"test-only-secret","truncated":false}`
				}
				if err := json.Unmarshal([]byte(payload), &msg.ToolResult.Result); err != nil {
					t.Fatal(err)
				}
			} else {
				name, version, field := "regression_search", "v1", "q"
				switch variant {
				case "unregistered":
					name = "unknown_tool"
				case "empty_policy":
					version = ""
				case "sensitive_call_field":
					field = "Authorization"
				}
				data, err := json.Marshal(map[string]any{
					"id": "call-1", "name": name, "policyVersion": version,
					"fields": []conversation.SafeField{{Name: field, Value: "test-only-secret"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				var call conversation.SafeToolCall
				if err := json.Unmarshal(data, &call); err != nil {
					t.Fatal(err)
				}
				msg = conversation.EntryMessage{
					Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools,
					Content: []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}},
				}
			}
			result, err := repo.Append(ctx, conversation.AppendEntryInput{
				ConversationID: "regression", ExpectedHead: regressionHeadToken(currentHead), Message: msg,
			})
			if !errors.Is(err, domainerr.ErrInvalidInput) || !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
				t.Fatalf("unsafe payload accepted or result leaked: %+v, %v", result, err)
			}
			assertRegressionTreeState(t, ctx, db, repo, currentHead, nodes)
			var leaked int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversation_entry_nodes WHERE payload_json LIKE '%test-only-secret%'").Scan(&leaked); err != nil {
				t.Fatal(err)
			}
			if leaked != 0 {
				t.Fatal("rejected data persisted")
			}
		})
	}
}

func TestEntryTreeRejectsCorruptHistoryOnReadMoveAndAppend(t *testing.T) {
	for _, variant := range []string{"orphan", "extra_result", "corrupt_group_before_user"} {
		t.Run(variant, func(t *testing.T) {
			registerRegressionToolPolicy(t)
			db, ctx, repo := newRegressionEntryTree(t)
			root := appendRegressionMessage(t, ctx, repo, conversation.EntryHead{}, regressionUserMessage())
			last := root
			if variant != "orphan" {
				assistant := appendRegressionMessage(t, ctx, repo, root.Head, regressionToolMessage(t))
				last = appendRegressionMessage(t, ctx, repo, assistant.Head, regressionToolResult(t, assistant.Entry.ID))
			}
			insertCorruptChild := func(id conversation.EntryID, parent conversation.ConversationEntry, message conversation.EntryMessage) conversation.ConversationEntry {
				t.Helper()
				data, err := sqlite.EncodeEntryMessage(message)
				if err != nil {
					t.Fatal(err)
				}
				_, err = db.ExecContext(ctx, `INSERT INTO conversation_entry_nodes
					(id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at)
					VALUES (?, 'regression', ?, 'message', 1, ?, ?, ?, ?)`,
					string(id), string(parent.ID), string(data), parent.AppendIndex+1, parent.Depth+1,
					time.Now().UTC().Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
				return conversation.ConversationEntry{ID: id, AppendIndex: parent.AppendIndex + 1, Depth: parent.Depth + 1}
			}
			corrupt := insertCorruptChild("e_corrupt_result", last.Entry, regressionToolResult(t, root.Entry.ID))
			if variant == "corrupt_group_before_user" {
				corrupt = insertCorruptChild("e_user_after_corruption", corrupt, regressionUserMessage())
			}
			_, err := repo.ReadPath(ctx, conversation.PathQuery{ConversationID: "regression", LeafID: &corrupt.ID})
			if !errors.Is(err, conversation.ErrEntryCorrupt) {
				t.Fatalf("ReadPath accepted %s: %v", variant, err)
			}
			_, err = repo.MoveHead(ctx, conversation.MoveHeadInput{
				ConversationID: "regression", ExpectedHead: regressionHeadToken(last.Head), TargetID: &corrupt.ID,
			})
			if !errors.Is(err, conversation.ErrEntryCorrupt) {
				t.Fatalf("MoveHead accepted %s: %v", variant, err)
			}
			assertRegressionTreeState(t, ctx, db, repo, last.Head, int(corrupt.AppendIndex))
			// Simulate a pre-existing bad head: appending must not hide or extend
			// the damaged history even though the next user message is valid.
			if _, err := db.ExecContext(ctx, "UPDATE conversation_entry_heads SET active_leaf_id = ?, version = version + 1 WHERE conversation_id = ?", string(corrupt.ID), "regression"); err != nil {
				t.Fatal(err)
			}
			badHead, err := repo.GetHead(ctx, "regression")
			if err != nil {
				t.Fatal(err)
			}
			result, err := repo.Append(ctx, conversation.AppendEntryInput{
				ConversationID: "regression", ExpectedHead: regressionHeadToken(badHead), Message: regressionUserMessage(),
			})
			if !errors.Is(err, conversation.ErrEntryCorrupt) || !reflect.DeepEqual(result, conversation.AppendEntryResult{}) {
				t.Fatalf("Append extended corrupt history: %+v, %v", result, err)
			}
			assertRegressionTreeState(t, ctx, db, repo, badHead, int(corrupt.AppendIndex))
		})
	}
}
