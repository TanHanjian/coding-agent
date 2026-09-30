package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func mustProjectCall(t *testing.T, toolName, version, callID, rawJSON string) conversation.SafeToolCall {
	t.Helper()
	p, ok := conversation.GetToolPolicy(toolName, version)
	if !ok {
		t.Fatalf("policy %s@%s not registered", toolName, version)
	}
	tc, err := conversation.ProjectSafeToolCall(p, callID, json.RawMessage(rawJSON))
	if err != nil {
		t.Fatalf("ProjectSafeToolCall failed: %v", err)
	}
	return tc
}

func mustProjectResult(t *testing.T, toolName, version, rawJSON string) conversation.SafeToolResult {
	t.Helper()
	p, ok := conversation.GetToolPolicy(toolName, version)
	if !ok {
		t.Fatalf("policy %s@%s not registered", toolName, version)
	}
	tr, err := conversation.ProjectSafeToolResult(p, json.RawMessage(rawJSON))
	if err != nil {
		t.Fatalf("ProjectSafeToolResult failed: %v", err)
	}
	return tr
}

func createSQLConversation(t *testing.T, ctx context.Context, db *storage.DB, id string) {
	t.Helper()
	now := time.Now().UTC()
	repo := sqlite.NewConversationRepository(db)
	if err := repo.Create(ctx, conversation.Conversation{
		ID:        id,
		Title:     "conv " + id,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func stringRef(s string) *string {
	return &s
}

func TestEntryTreeLazyHeadAndAppend(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c1")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// 1. GetHead for empty conversation returns version 0 and nil active leaf
	head, err := repo.GetHead(ctx, "c1")
	if err != nil {
		t.Fatalf("GetHead failed: %v", err)
	}
	if head.ActiveLeafID != nil || head.Version != 0 {
		t.Fatalf("expected initial head (nil, 0), got (%v, %d)", head.ActiveLeafID, head.Version)
	}

	// 2. Append first root message (user)
	userMsg := conversation.EntryMessage{
		Role: conversation.EntryRoleUser,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringRef("Hello, world!")},
		},
	}
	res1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c1",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message:        userMsg,
	})
	if err != nil {
		t.Fatalf("Append root failed: %v", err)
	}
	if res1.Entry.Depth != 0 || res1.Entry.AppendIndex != 1 || res1.Entry.ParentID != nil {
		t.Fatalf("unexpected root entry: %+v", res1.Entry)
	}
	if res1.Head.Version != 1 || res1.Head.ActiveLeafID == nil || *res1.Head.ActiveLeafID != res1.Entry.ID {
		t.Fatalf("unexpected head after root append: %+v", res1.Head)
	}

	// 3. Append assistant message
	asstMsg := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringRef("Hi there! How can I help?")},
		},
	}
	res2, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c1",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &res1.Entry.ID, Version: 1},
		Message:        asstMsg,
	})
	if err != nil {
		t.Fatalf("Append assistant failed: %v", err)
	}
	if res2.Entry.Depth != 1 || res2.Entry.AppendIndex != 2 || res2.Entry.ParentID == nil || *res2.Entry.ParentID != res1.Entry.ID {
		t.Fatalf("unexpected assistant entry: %+v", res2.Entry)
	}
	if res2.Head.Version != 2 || *res2.Head.ActiveLeafID != res2.Entry.ID {
		t.Fatalf("unexpected head after assistant: %+v", res2.Head)
	}

	// 4. ReadPath active leaf
	path, err := repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c1",
		UseActiveLeaf:  true,
	})
	if err != nil {
		t.Fatalf("ReadPath failed: %v", err)
	}
	if len(path.Entries) != 2 {
		t.Fatalf("expected 2 entries in path, got %d", len(path.Entries))
	}
	if path.Entries[0].ID != res1.Entry.ID || path.Entries[1].ID != res2.Entry.ID {
		t.Fatalf("unexpected path entries: %+v", path.Entries)
	}

	// 5. ReadPath specific historical leaf
	path1, err := repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c1",
		LeafID:         &res1.Entry.ID,
	})
	if err != nil {
		t.Fatalf("ReadPath historical leaf failed: %v", err)
	}
	if len(path1.Entries) != 1 || path1.Entries[0].ID != res1.Entry.ID {
		t.Fatalf("unexpected historical path: %+v", path1.Entries)
	}

	// 6. Get single entry
	gotEntry, err := repo.Get(ctx, conversation.EntryRef{
		ConversationID: "c1",
		ID:             res1.Entry.ID,
	})
	if err != nil {
		t.Fatalf("Get entry failed: %v", err)
	}
	if gotEntry.ID != res1.Entry.ID {
		t.Fatalf("got wrong entry: %+v", gotEntry)
	}
}

func TestEntryTreeBranchingAndMoveHead(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_branch")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Build: U1 -> A1 -> U2 -> A2
	u1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("U1")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	a1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u1.Entry.ID, Version: u1.Head.Version},
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("A1")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	u2, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: a1.Head.Version},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("U2")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	a2, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u2.Entry.ID, Version: u2.Head.Version},
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("A2")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// MoveHead to A1
	moveHead, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a2.Entry.ID, Version: a2.Head.Version},
		TargetID:       &a1.Entry.ID,
	})
	if err != nil {
		t.Fatalf("MoveHead failed: %v", err)
	}
	if *moveHead.ActiveLeafID != a1.Entry.ID || moveHead.Version != a2.Head.Version+1 {
		t.Fatalf("unexpected head after MoveHead: %+v", moveHead)
	}

	// Append U3 from A1
	u3, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: moveHead.Version},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("U3")}},
		},
	})
	if err != nil {
		t.Fatalf("Append U3 failed: %v", err)
	}
	if *u3.Entry.ParentID != a1.Entry.ID || u3.Entry.Depth != 2 || u3.Entry.AppendIndex != 5 {
		t.Fatalf("unexpected entry for U3: %+v", u3.Entry)
	}

	// Verify active path is [U1, A1, U3]
	activePath, err := repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c_branch",
		UseActiveLeaf:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(activePath.Entries) != 3 || activePath.Entries[0].ID != u1.Entry.ID || activePath.Entries[1].ID != a1.Entry.ID || activePath.Entries[2].ID != u3.Entry.ID {
		t.Fatalf("unexpected active path: %+v", activePath.Entries)
	}

	// Verify old path [U1, A1, U2, A2] is intact!
	oldPath, err := repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c_branch",
		LeafID:         &a2.Entry.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(oldPath.Entries) != 4 || oldPath.Entries[3].ID != a2.Entry.ID {
		t.Fatalf("unexpected old path: %+v", oldPath.Entries)
	}

	// Reset head to nil and append a new root U4
	resetHead, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u3.Entry.ID, Version: u3.Head.Version},
		TargetID:       nil,
	})
	if err != nil {
		t.Fatalf("reset to nil failed: %v", err)
	}
	if resetHead.ActiveLeafID != nil {
		t.Fatalf("expected nil active leaf after reset")
	}

	u4, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_branch",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: resetHead.Version},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("U4")}},
		},
	})
	if err != nil {
		t.Fatalf("append second root failed: %v", err)
	}
	if u4.Entry.Depth != 0 || u4.Entry.ParentID != nil {
		t.Fatalf("unexpected second root entry: %+v", u4.Entry)
	}
}

func TestEntryTreeToolGroupContinuation(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_tool")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Register policy
	conversation.ClearToolPolicies()
	policy := conversation.ToolPersistencePolicy{
		ToolName: "search",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "q", StoredKey: "query", MaxBytes: 100},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "ans", StoredKey: "answer", MaxBytes: 100},
		},
		MaxPreviewBytes: 50,
	}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}

	// 1. Root user
	u1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("search something")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Assistant with 2 tool calls: c1 and c2
	tc1 := mustProjectCall(t, "search", "v1", "c1", `{"q":"val1"}`)
	tc2 := mustProjectCall(t, "search", "v1", "c2", `{"q":"val2"}`)
	a1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u1.Entry.ID, Version: u1.Head.Version},
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopTools,
			Content: []conversation.EntryPart{
				{Type: conversation.EntryPartToolCall, ToolCall: &tc1},
				{Type: conversation.EntryPartToolCall, ToolCall: &tc2},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. MoveHead to A1 -> must be rejected because tool group is unclosed!
	_, err = repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: a1.Head.Version},
		TargetID:       &a1.Entry.ID,
	})
	if !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput when moving head to unclosed tool group assistant, got %v", err)
	}

	// 4. Try appending user message -> rejected (cannot cross over pending group)
	_, err = repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: a1.Head.Version},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("interruption")}},
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict when appending user over pending tool group, got %v", err)
	}

	// 5. Append R1 (for c1)
	r1Res := mustProjectResult(t, "search", "v1", `{"ans":"found1"}`)
	r1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: a1.Head.Version},
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: a1.Entry.ID,
				CallID:      "c1",
				ToolName:    "search",
				Result:      r1Res,
			},
		},
	})
	if err != nil {
		t.Fatalf("Append R1 failed: %v", err)
	}

	// MoveHead to R1 -> rejected (still unclosed, need c2)
	_, err = repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &r1.Entry.ID, Version: r1.Head.Version},
		TargetID:       &r1.Entry.ID,
	})
	if !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput when moving head to partial tool results, got %v", err)
	}

	// 6. Append R2 (for c2)
	r2Res := mustProjectResult(t, "search", "v1", `{"ans":"found2"}`)
	r2, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &r1.Entry.ID, Version: r1.Head.Version},
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: a1.Entry.ID,
				CallID:      "c2",
				ToolName:    "search",
				Result:      r2Res,
			},
		},
	})
	if err != nil {
		t.Fatalf("Append R2 failed: %v", err)
	}

	// MoveHead to R2 -> accepted! Group is closed!
	moveHeadR2, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_tool",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &r2.Entry.ID, Version: r2.Head.Version},
		TargetID:       &r2.Entry.ID,
	})
	if err != nil {
		t.Fatalf("MoveHead to completed group failed: %v", err)
	}
	if *moveHeadR2.ActiveLeafID != r2.Entry.ID {
		t.Fatalf("unexpected active leaf: %+v", moveHeadR2)
	}

	// ReadPath
	path, err := repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c_tool",
		UseActiveLeaf:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Entries) != 4 {
		t.Fatalf("expected 4 entries in path, got %d", len(path.Entries))
	}
}

func TestEntryTreeStaleHeadCASAndABA(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_aba")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// P(v1)
	resP, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_aba",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("P")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenP_v1 := conversation.HeadToken{ActiveLeafID: &resP.Entry.ID, Version: resP.Head.Version}

	// Q(v2)
	resQ, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_aba",
		ExpectedHead:   tokenP_v1,
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("Q")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// MoveHead back to P -> P(v3)
	moveP, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_aba",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &resQ.Entry.ID, Version: resQ.Head.Version},
		TargetID:       &resP.Entry.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if moveP.Version != 3 || *moveP.ActiveLeafID != resP.Entry.ID {
		t.Fatalf("unexpected moveP head: %+v", moveP)
	}

	// Try appending using old token P(v1) -> ABA detected and rejected!
	_, err = repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_aba",
		ExpectedHead:   tokenP_v1,
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("Stale append")}},
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict on stale ABA token, got %v", err)
	}
}

func TestEntryTreeImmutabilityAndCascadeDelete(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_immut")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	res, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_immut",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("Immutable")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Direct UPDATE on entry node -> blocked by trigger
	_, err = db.Exec("UPDATE conversation_entry_nodes SET depth = 99 WHERE id = ?", string(res.Entry.ID))
	if err == nil {
		t.Fatal("expected error updating entry node, but it succeeded")
	}

	// 2. Direct individual DELETE on entry node -> blocked by trigger
	_, err = db.Exec("DELETE FROM conversation_entry_nodes WHERE id = ?", string(res.Entry.ID))
	if err == nil {
		t.Fatal("expected error individually deleting entry node, but it succeeded")
	}

	// 3. Delete Conversation -> cascade delete clears nodes and heads!
	convRepo := sqlite.NewConversationRepository(db)
	if err := convRepo.Delete(ctx, "c_immut"); err != nil {
		t.Fatalf("conversation delete failed: %v", err)
	}

	var nodeCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM conversation_entry_nodes WHERE conversation_id = ?", "c_immut").Scan(&nodeCount); err != nil {
		t.Fatal(err)
	}
	if nodeCount != 0 {
		t.Fatalf("expected 0 entry nodes after cascade, got %d", nodeCount)
	}

	var headCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM conversation_entry_heads WHERE conversation_id = ?", "c_immut").Scan(&headCount); err != nil {
		t.Fatal(err)
	}
	if headCount != 0 {
		t.Fatalf("expected 0 entry heads after cascade, got %d", headCount)
	}
}

func TestEntryTreeCompositeFK(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_fk1")
	createSQLConversation(t, ctx, db, "c_fk2")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Create node in c_fk1
	res1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_fk1",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("Node in FK1")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to insert node in c_fk2 referencing parent in c_fk1 via direct SQL
	_, err = db.Exec(`
		INSERT INTO conversation_entry_nodes (
			id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
		) VALUES (?, ?, ?, 'message', 1, '{"role":"user","content":[{"type":"text","text":"bad"}]}', 1, 1, ?)
	`, "e_cross", "c_fk2", string(res1.Entry.ID), time.Now().UTC().Format(time.RFC3339Nano))

	if err == nil {
		t.Fatal("expected foreign key failure when referencing parent in different conversation, but insert succeeded")
	}
}

func TestEntryTreeCapacityLimits(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_cap")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{
		MaxPayloadBytes: 100,
		MaxPathEntries:  2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Payload > 50 bytes
	longText := "This is a long user text that definitely exceeds fifty bytes in payload"
	_, err = repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_cap",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &longText}},
		},
	})
	if !errors.Is(err, conversation.ErrEntryCapacity) {
		t.Fatalf("expected ErrEntryCapacity for large payload, got %v", err)
	}

	// Now append valid small node 1
	shortText1 := "small 1"
	res1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_cap",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &shortText1}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Node 2 (depth 1, path length 2 <= MaxPathEntries)
	shortText2 := "small 2"
	res2, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_cap",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &res1.Entry.ID, Version: res1.Head.Version},
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &shortText2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Node 3 (depth 2, path length 3 > MaxPathEntries) -> capacity exceeded
	shortText3 := "small 3"
	_, err = repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_cap",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &res2.Entry.ID, Version: res2.Head.Version},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &shortText3}},
		},
	})
	if !errors.Is(err, conversation.ErrEntryCapacity) {
		t.Fatalf("expected ErrEntryCapacity when exceeding max path entries, got %v", err)
	}
}

func TestEntryTreeCodec_StrictAndDuplicateKeys(t *testing.T) {
	// 1. Duplicate JSON key
	dupJSON := []byte(`{"role":"user","role":"assistant","content":[{"type":"text","text":"hello"}]}`)
	_, err := sqlite.DecodeEntryMessage(conversation.EntryPayloadV1, dupJSON)
	if !errors.Is(err, conversation.ErrEntryCorrupt) {
		t.Fatalf("expected ErrEntryCorrupt for duplicate key, got %v", err)
	}

	// 2. Unsupported payload version
	validJSON := []byte(`{"role":"user","content":[{"type":"text","text":"hello"}]}`)
	_, err = sqlite.DecodeEntryMessage(99, validJSON)
	if !errors.Is(err, conversation.ErrEntryUnsupportedVersion) {
		t.Fatalf("expected ErrEntryUnsupportedVersion, got %v", err)
	}

	// 3. Unknown field
	unknownFieldJSON := []byte(`{"role":"user","unknown_field":"val","content":[{"type":"text","text":"hello"}]}`)
	_, err = sqlite.DecodeEntryMessage(conversation.EntryPayloadV1, unknownFieldJSON)
	if !errors.Is(err, conversation.ErrEntryCorrupt) {
		t.Fatalf("expected ErrEntryCorrupt for unknown field, got %v", err)
	}
}

func TestEntryTreeChildrenPagination(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_children")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Create root U1
	u1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_children",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("U1")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create 3 branch children under U1: A1, A2, A3
	var childIDs []conversation.EntryID
	currentHead := u1.Head
	for i := 1; i <= 3; i++ {
		text := fmt.Sprintf("Assistant branch %d", i)
		asst, err := repo.Append(ctx, conversation.AppendEntryInput{
			ConversationID: "c_children",
			ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u1.Entry.ID, Version: currentHead.Version},
			Message: conversation.EntryMessage{
				Role:       conversation.EntryRoleAssistant,
				StopReason: conversation.EntryStopNormal,
				Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		childIDs = append(childIDs, asst.Entry.ID)
		currentHead = asst.Head

		// Move head back to U1 for next branch child
		if i < 3 {
			moved, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
				ConversationID: "c_children",
				ExpectedHead:   conversation.HeadToken{ActiveLeafID: &asst.Entry.ID, Version: currentHead.Version},
				TargetID:       &u1.Entry.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			currentHead = moved
		}
	}

	// List children of U1 with limit = 2
	page1, err := repo.ListChildren(ctx, conversation.ChildrenQuery{
		ConversationID: "c_children",
		ParentID:       &u1.Entry.ID,
		Limit:          2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Entries) != 2 || page1.NextIndex == nil {
		t.Fatalf("expected 2 entries and non-nil NextIndex, got %d, %v", len(page1.Entries), page1.NextIndex)
	}
	if page1.Entries[0].ID != childIDs[0] || page1.Entries[1].ID != childIDs[1] {
		t.Fatalf("unexpected page 1 entries: %+v", page1.Entries)
	}

	// Page 2 with AfterIndex
	page2, err := repo.ListChildren(ctx, conversation.ChildrenQuery{
		ConversationID: "c_children",
		ParentID:       &u1.Entry.ID,
		AfterIndex:     *page1.NextIndex,
		Limit:          2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Entries) != 1 || page2.NextIndex != nil {
		t.Fatalf("expected 1 entry and nil NextIndex, got %d, %v", len(page2.Entries), page2.NextIndex)
	}
	if page2.Entries[0].ID != childIDs[2] {
		t.Fatalf("unexpected page 2 entry: %+v", page2.Entries[0])
	}
}

func TestEntryTreeInsertOrReplaceBlocked(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_repl")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	res, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_repl",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("original node")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try INSERT OR REPLACE on the existing node ID
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
		INSERT OR REPLACE INTO conversation_entry_nodes (
			id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
		) VALUES (?, ?, NULL, 'message', 1, '{"role":"user","content":[{"type":"text","text":"replaced text"}]}', 1, 0, ?)
	`, string(res.Entry.ID), "c_repl", nowStr)

	if err == nil {
		t.Fatal("expected INSERT OR REPLACE to be blocked by trigger, but it succeeded")
	}

	// Verify original entry was NOT replaced
	entry, err := repo.Get(ctx, conversation.EntryRef{ConversationID: "c_repl", ID: res.Entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	if *entry.Message.Content[0].Text != "original node" {
		t.Fatalf("entry content was replaced! Got: %s", *entry.Message.Content[0].Text)
	}
}

func TestEntryTreeZeroValueOnError(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_zero")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Append with mismatched head token -> must return zero AppendEntryResult
	res, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_zero",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 999}, // invalid version
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("fail")}},
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if res.Entry.ID != "" || res.Head.Version != 0 || res.Head.ConversationID != "" {
		t.Fatalf("expected zero-value AppendEntryResult on error, got %+v", res)
	}

	// MoveHead with mismatched head token -> must return zero EntryHead
	head, err := repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_zero",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 999},
		TargetID:       nil,
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if head.Version != 0 || head.ConversationID != "" || head.ActiveLeafID != nil {
		t.Fatalf("expected zero-value EntryHead on error, got %+v", head)
	}
}

func TestEntryTreeCorruptedToolGroupInPath(t *testing.T) {
	db, ctx := newSQLTestDB(t)
	createSQLConversation(t, ctx, db, "c_corrupt")

	repo, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}

	conversation.ClearToolPolicies()
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName:       "search",
		Version:        "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 100}},
		ResultFields:   []conversation.ProjectionFieldRule{{SourceKey: "ans", StoredKey: "ans", MaxBytes: 100}},
	})

	u1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_corrupt",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: nil, Version: 0},
		Message: conversation.EntryMessage{
			Role:    conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringRef("u1")}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tc := mustProjectCall(t, "search", "v1", "c1", `{"q":"test"}`)
	a1, err := repo.Append(ctx, conversation.AppendEntryInput{
		ConversationID: "c_corrupt",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &u1.Entry.ID, Version: u1.Head.Version},
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopTools,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &tc}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Direct insert of a corrupted toolResult (wrong ToolName "wrong_tool_name" != "search")
	nowStr := time.Now().UTC().Format(time.RFC3339Nano)
	corruptID := "e_corrupt_res"
	corruptJSONBad := `{"role":"toolResult","toolResult":{"callEntryId":"` + string(a1.Entry.ID) + `","callId":"c1","toolName":"wrong_tool_name","isError":false,"result":{"policyVersion":"v1","fields":[{"name":"ans","value":"ok"}],"preview":"ok","truncated":false}}}`

	// Register wrong_tool_name so JSON decode doesn't fail on policy unregistered
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName:     "wrong_tool_name",
		Version:      "v1",
		ResultFields: []conversation.ProjectionFieldRule{{SourceKey: "ans", StoredKey: "ans", MaxBytes: 100}},
	})

	_, err = db.Exec(`
		INSERT INTO conversation_entry_nodes (
			id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
		) VALUES (?, ?, ?, 'message', 1, ?, 3, 2, ?)
	`, corruptID, "c_corrupt", string(a1.Entry.ID), corruptJSONBad, nowStr)
	if err != nil {
		t.Fatal(err)
	}

	corruptEID := conversation.EntryID(corruptID)

	// ReadPath to the corrupted node -> must fail with ErrEntryCorrupt!
	_, err = repo.ReadPath(ctx, conversation.PathQuery{
		ConversationID: "c_corrupt",
		LeafID:         &corruptEID,
	})
	if !errors.Is(err, conversation.ErrEntryCorrupt) {
		t.Fatalf("expected ErrEntryCorrupt when reading path with corrupted tool result, got %v", err)
	}

	// MoveHead to corrupted node -> must be rejected!
	_, err = repo.MoveHead(ctx, conversation.MoveHeadInput{
		ConversationID: "c_corrupt",
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: &a1.Entry.ID, Version: a1.Head.Version},
		TargetID:       &corruptEID,
	})
	if err == nil {
		t.Fatal("expected error when moving head to corrupted tool result, but it succeeded")
	}
}
