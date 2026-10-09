package sqlite

import (
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/agentrun"
)

func TestAgentRunORMReadsIndependentLegacyFixture(t *testing.T) {
	db, ctx, repo := runFixture(t)
	at := "2026-05-06T07:08:09.123456789+08:00"
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO messages (id, conversation_id, client_message_id, sequence, role, content, status, error_code, error_message, created_at, updated_at)
		  VALUES ('old-user', 'entry-tx', 'old-client', 1, 'user', 'hello user', 'completed', '', '', ?, ?),
		         ('old-assistant', 'entry-tx', '', 2, 'assistant', '', 'streaming', '', '', ?, ?)`, []any{at, at, at, at}},
		{`INSERT INTO conversation_entry_nodes (id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at)
		  VALUES ('old-user-entry', 'entry-tx', NULL, 'message', 1, '{"role":"user","content":[{"type":"text","text":"hello user"}]}', 1, 0, ?)`, []any{at}},
		{`INSERT INTO conversation_entry_heads (conversation_id, active_leaf_id, version, updated_at)
		  VALUES ('entry-tx', 'old-user-entry', 1, ?)`, []any{at}},
		{`INSERT INTO agent_runs (id, conversation_id, client_message_id, user_message_id, assistant_message_id, base_entry_id, base_head_version,
		  user_entry_id, last_entry_id, last_closed_entry_id, final_entry_id, head_version, status, error_code, created_at, started_at, finished_at)
		  VALUES ('old-run', 'entry-tx', 'old-client', 'old-user', 'old-assistant', NULL, 0, 'old-user-entry', 'old-user-entry', 'old-user-entry', NULL, 1, 'pending', '', ?, NULL, NULL)`, []any{at}},
	} {
		if _, err := db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	result, err := repo.BeginTurn(ctx, runInput("old-client"))
	if err != nil || !result.Reused || result.Run == nil {
		t.Fatalf("legacy retry = %+v, %v", result, err)
	}
	run := result.Run
	wantTime, _ := time.Parse(time.RFC3339Nano, at)
	if run.ID != "old-run" || run.BaseEntryID != nil || run.FinalEntryID != nil || run.StartedAt != nil || run.FinishedAt != nil || run.Status != agentrun.StatusPending ||
		!run.CreatedAt.Equal(wantTime) || run.CreatedAt.Location() != time.UTC {
		t.Fatalf("legacy run mapping = %+v", run)
	}
	assertRunCounts(t, ctx, db, 2, 1, 1, 1)
}
