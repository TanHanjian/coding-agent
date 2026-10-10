package agentcontext

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func projectionFixture(t *testing.T) conversation.ProjectionSnapshot {
	t.Helper()
	policy := conversation.ToolPersistencePolicy{ToolName: "projection-test", Version: "v1", ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "query", StoredKey: "query", MaxBytes: 80}}, ResultFields: []conversation.ProjectionFieldRule{{SourceKey: "answer", StoredKey: "answer", MaxBytes: 120}}, MaxPreviewBytes: 80}
	if _, exists := conversation.GetToolPolicy(policy.ToolName, policy.Version); !exists {
		if err := conversation.RegisterToolPolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	text := "question"
	entries := []conversation.ConversationEntry{{Message: &conversation.EntryMessage{Role: conversation.EntryRoleUser, Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}}}}}
	assistant := conversation.EntryMessage{Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools, Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}}}
	for _, id := range []string{"c1", "c2"} {
		call, err := conversation.ProjectSafeToolCall(policy, id, json.RawMessage(`{"query":"safe","token":"must-not-appear"}`))
		if err != nil {
			t.Fatal(err)
		}
		assistant.Content = append(assistant.Content, conversation.EntryPart{Type: conversation.EntryPartToolCall, ToolCall: &call})
	}
	entries = append(entries, conversation.ConversationEntry{Message: &assistant})
	result, err := conversation.ProjectSafeToolResult(policy, json.RawMessage(`{"answer":"safe result","token":"must-not-appear"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2"} {
		entries = append(entries, conversation.ConversationEntry{Message: &conversation.EntryMessage{Role: conversation.EntryRoleToolResult, ToolResult: &conversation.EntryToolResult{CallEntryID: "e_1", CallID: id, ToolName: policy.ToolName, IsError: id == "c2", Result: result}}})
	}
	for i := range entries {
		entries[i].ID, entries[i].ConversationID, entries[i].Kind, entries[i].PayloadVersion, entries[i].Depth = conversation.EntryID(fmt.Sprintf("e_%d", i)), "conv", conversation.EntryKindMessage, conversation.EntryPayloadV1, int64(i)
		if i > 0 {
			parent := entries[i-1].ID
			entries[i].ParentID = &parent
		}
	}
	return conversation.ProjectionSnapshot{Head: conversation.EntryHead{ConversationID: "conv"}, Entries: entries}
}

func TestProjectSnapshotSafeWholeGroup(t *testing.T) {
	snapshot := projectionFixture(t)
	history, err := ProjectSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %+v", history)
	}
	group := history[1]
	if group.Role != conversation.MessageRoleAssistant || group.DowngradePolicy != conversation.ToolHistoryPolicyVersion || len(group.Sources) != 4 || group.Sources[3].CallID != "c2" || !group.Sources[3].IsError {
		t.Fatalf("group = %+v", group)
	}
	if !strings.Contains(group.Content, "NOT original arguments") || !strings.Contains(group.Content, `"isError":true`) || strings.Contains(group.Content, "must-not-appear") {
		t.Fatalf("content = %s", group.Content)
	}
	clone := conversation.CloneContextMessages(history)
	clone[1].Sources[0].CallID = "changed"
	if history[1].Sources[0].CallID != "c1" {
		t.Fatal("source alias")
	}
}

func TestProjectSnapshotRejectsInvalidFacts(t *testing.T) {
	for _, name := range []string{"pending", "duplicate", "orphan", "wrong name", "unknown version", "cross conversation", "missing parent", "unsupported block", "unknown policy"} {
		t.Run(name, func(t *testing.T) {
			snapshot := projectionFixture(t)
			switch name {
			case "pending":
				snapshot.Entries = snapshot.Entries[:3]
			case "duplicate":
				snapshot.Entries[3].Message.ToolResult.CallID = "c1"
			case "orphan":
				snapshot.Entries[1].Message.Content = snapshot.Entries[1].Message.Content[:1]
				snapshot.Entries[1].Message.StopReason = conversation.EntryStopNormal
			case "wrong name":
				snapshot.Entries[2].Message.ToolResult.ToolName = "other"
			case "unknown version":
				snapshot.Entries[0].PayloadVersion = 99
			case "cross conversation":
				snapshot.Entries[0].ConversationID = "other"
			case "missing parent":
				snapshot.Entries[2].ParentID = nil
			case "unsupported block":
				snapshot.Entries[0].Message.Content[0].Type = "media"
			case "unknown policy":
				if err := json.Unmarshal([]byte(`{"id":"c1","name":"projection-test","policyVersion":"unknown","fields":[]}`), snapshot.Entries[1].Message.Content[1].ToolCall); err != nil {
					t.Fatal(err)
				}
			}
			history, err := ProjectSnapshot(snapshot)
			if err == nil || history != nil {
				t.Fatalf("history = %+v, err = %v", history, err)
			}
			if name == "unknown version" && !errors.Is(err, conversation.ErrEntryUnsupportedVersion) {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectSnapshotUTF8GroupBound(t *testing.T) {
	snapshot := projectionFixture(t)
	longText := strings.Repeat("中", maxToolHistoryBytes)
	snapshot.Entries[1].Message.Content[0].Text = &longText
	history, err := ProjectSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	group := history[1]
	if !group.Truncated || len(group.Content) > maxToolHistoryBytes || !utf8.ValidString(group.Content) || !strings.HasSuffix(group.Content, "not complete results.]") || len(group.Sources) != 4 {
		t.Fatalf("group len=%d sources=%+v", len(group.Content), group.Sources)
	}
}

func TestProjectSnapshotLegacySourcesAndEmptyHistory(t *testing.T) {
	snapshot := conversation.ProjectionSnapshot{Head: conversation.EntryHead{ConversationID: "conv"}, LegacyBeforeSequence: 3, Legacy: []conversation.Message{{ID: "m1", ConversationID: "conv", Role: conversation.MessageRoleUser, Status: conversation.MessageStatusCompleted, Sequence: 1, Content: "old"}}}
	history, err := ProjectSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if history[0].Sources[0].MessageID != "m1" || history[0].Sources[0].EntryID != "" {
		t.Fatalf("source = %+v", history)
	}
	snapshot.LegacyBeforeSequence = 1
	if _, err := ProjectSnapshot(snapshot); err == nil {
		t.Fatal("accepted duplicate coverage boundary")
	}
	empty, err := ProjectSnapshot(conversation.ProjectionSnapshot{})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v %v", empty, err)
	}
}
