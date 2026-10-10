package eino

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestConvertAssistantMessage_TextOnly(t *testing.T) {
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "Hello, how can I help you?",
	}

	entry, err := ConvertAssistantMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if entry.Role != conversation.EntryRoleAssistant {
		t.Errorf("expected role assistant, got %s", entry.Role)
	}
	if len(entry.Content) != 1 {
		t.Fatalf("expected 1 part, got %d", len(entry.Content))
	}
	if entry.Content[0].Type != conversation.EntryPartText {
		t.Errorf("expected text part, got %s", entry.Content[0].Type)
	}
	if *entry.Content[0].Text != "Hello, how can I help you?" {
		t.Errorf("unexpected text: %s", *entry.Content[0].Text)
	}
	if entry.StopReason != conversation.EntryStopNormal {
		t.Errorf("expected stop reason normal, got %s", entry.StopReason)
	}
}

func TestConvertAssistantMessage_RequiresAssistantRole(t *testing.T) {
	msg := &schema.Message{
		Role:    schema.User,
		Content: "test",
	}

	_, err := ConvertAssistantMessage(msg)
	if err == nil {
		t.Fatal("expected error for non-assistant role")
	}
}

func TestConvertAssistantMessage_RequiresContentOrToolCalls(t *testing.T) {
	msg := &schema.Message{
		Role: schema.Assistant,
	}

	_, err := ConvertAssistantMessage(msg)
	if err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestConvertAssistantMessage_WithToolCalls(t *testing.T) {
	// Register a test policy
	policy := conversation.ToolPersistencePolicy{
		ToolName: "test_tool",
		Version:  "1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "query", StoredKey: "query", MaxBytes: 1000},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "result", StoredKey: "result", MaxBytes: 5000},
		},
		MaxPreviewBytes: 500,
	}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatalf("failed to register policy: %v", err)
	}
	defer conversation.ClearToolPolicies()

	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "Let me search for that.",
		ToolCalls: []schema.ToolCall{
			{
				ID: "call_123",
				Function: schema.FunctionCall{
					Name:      "test_tool",
					Arguments: `{"query":"test search"}`,
				},
			},
		},
	}

	entry, err := ConvertAssistantMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entry.Content) != 2 {
		t.Fatalf("expected 2 parts (text + tool call), got %d", len(entry.Content))
	}

	// First part should be text
	if entry.Content[0].Type != conversation.EntryPartText {
		t.Errorf("expected first part to be text, got %s", entry.Content[0].Type)
	}

	// Second part should be tool call
	if entry.Content[1].Type != conversation.EntryPartToolCall {
		t.Errorf("expected second part to be tool call, got %s", entry.Content[1].Type)
	}

	toolCall := entry.Content[1].ToolCall
	if toolCall == nil {
		t.Fatal("tool call is nil")
	}
	if toolCall.ID() != "call_123" {
		t.Errorf("expected call ID call_123, got %s", toolCall.ID())
	}
	if toolCall.Name() != "test_tool" {
		t.Errorf("expected tool name test_tool, got %s", toolCall.Name())
	}

	if entry.StopReason != conversation.EntryStopTools {
		t.Errorf("expected stop reason toolUse, got %s", entry.StopReason)
	}
}

func TestConvertAssistantMessage_UnregisteredToolPolicy(t *testing.T) {
	conversation.ClearToolPolicies()

	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{
				ID: "call_123",
				Function: schema.FunctionCall{
					Name:      "unregistered_tool",
					Arguments: `{"query":"test"}`,
				},
			},
		},
	}

	_, err := ConvertAssistantMessage(msg)
	if err == nil {
		t.Fatal("expected error for unregistered tool policy")
	}
}

func TestConvertToolResultMessages_Success(t *testing.T) {
	// Register policy
	policy := conversation.ToolPersistencePolicy{
		ToolName: "test_tool",
		Version:  "1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "query", StoredKey: "query", MaxBytes: 1000},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "result", StoredKey: "result", MaxBytes: 5000},
		},
		MaxPreviewBytes: 500,
	}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatalf("failed to register policy: %v", err)
	}
	defer conversation.ClearToolPolicies()

	callEntryID := conversation.EntryID("e_test123")
	pendingCalls := map[string]string{
		"call_1": "test_tool",
		"call_2": "test_tool",
	}

	results := []*schema.Message{
		{
			Role:       schema.Tool,
			ToolCallID: "call_1",
			Content:    `{"result":"answer 1"}`,
		},
		{
			Role:       schema.Tool,
			ToolCallID: "call_2",
			Content:    `{"result":"answer 2"}`,
		},
	}

	entries, err := ConvertToolResultMessages(results, callEntryID, pendingCalls)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for i, entry := range entries {
		if entry.Role != conversation.EntryRoleToolResult {
			t.Errorf("entry %d: expected role toolResult, got %s", i, entry.Role)
		}
		if entry.ToolResult == nil {
			t.Fatalf("entry %d: tool result is nil", i)
		}
		if entry.ToolResult.CallEntryID != callEntryID {
			t.Errorf("entry %d: expected call entry ID %s, got %s", i, callEntryID, entry.ToolResult.CallEntryID)
		}
		if entry.ToolResult.ToolName != "test_tool" {
			t.Errorf("entry %d: expected tool name test_tool, got %s", i, entry.ToolResult.ToolName)
		}
	}
}

func TestConvertToolResultMessages_RequiresCallEntryID(t *testing.T) {
	results := []*schema.Message{
		{
			Role:       schema.Tool,
			ToolCallID: "call_1",
			Content:    "{}",
		},
	}

	_, err := ConvertToolResultMessages(results, "", map[string]string{"call_1": "test_tool"})
	if err == nil {
		t.Fatal("expected error for empty call entry ID")
	}
}

func TestConvertToolResultMessages_RequiresPendingCalls(t *testing.T) {
	results := []*schema.Message{
		{
			Role:       schema.Tool,
			ToolCallID: "call_1",
			Content:    "{}",
		},
	}

	_, err := ConvertToolResultMessages(results, "e_test", nil)
	if err == nil {
		t.Fatal("expected error for nil pending calls")
	}

	_, err = ConvertToolResultMessages(results, "e_test", map[string]string{})
	if err == nil {
		t.Fatal("expected error for empty pending calls")
	}
}

func TestConvertToolResultMessages_ValidatesToolCallID(t *testing.T) {
	pendingCalls := map[string]string{"call_1": "test_tool"}

	results := []*schema.Message{
		{
			Role:       schema.Tool,
			ToolCallID: "call_999", // Not in pending set
			Content:    "{}",
		},
	}

	_, err := ConvertToolResultMessages(results, "e_test", pendingCalls)
	if err == nil {
		t.Fatal("expected error for tool call ID not in pending set")
	}
}

func TestConvertToolResultMessages_RequiresToolRole(t *testing.T) {
	pendingCalls := map[string]string{"call_1": "test_tool"}

	results := []*schema.Message{
		{
			Role:       schema.Assistant, // Wrong role
			ToolCallID: "call_1",
			Content:    "{}",
		},
	}

	_, err := ConvertToolResultMessages(results, "e_test", pendingCalls)
	if err == nil {
		t.Fatal("expected error for non-tool role")
	}
}
