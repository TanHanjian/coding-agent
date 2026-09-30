package conversation_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func stringPtr(s string) *string {
	return &s
}

func setupTestPolicies(t *testing.T) {
	t.Helper()
	conversation.ClearToolPolicies()
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName: "search",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "q", StoredKey: "q", MaxBytes: 100},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "count", StoredKey: "count", MaxBytes: 100},
			{SourceKey: "summary", StoredKey: "summary", MaxBytes: 100},
		},
		MaxPreviewBytes: 100,
	})
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName: "calc",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "expr", StoredKey: "expr", MaxBytes: 100},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "val", StoredKey: "val", MaxBytes: 100},
		},
		MaxPreviewBytes: 100,
	})
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName: "tool1",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "arg", StoredKey: "arg", MaxBytes: 100},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "res", StoredKey: "res", MaxBytes: 100},
		},
		MaxPreviewBytes: 100,
	})
	_ = conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName: "tool2",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "arg", StoredKey: "arg", MaxBytes: 100},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "res", StoredKey: "res", MaxBytes: 100},
		},
		MaxPreviewBytes: 100,
	})
}

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

func TestValidateEntryMessage_User(t *testing.T) {
	setupTestPolicies(t)

	// Valid user message
	validUser := conversation.EntryMessage{
		Role: conversation.EntryRoleUser,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("hello world")},
		},
	}
	if err := conversation.ValidateEntryMessage(validUser); err != nil {
		t.Fatalf("expected valid user message, got %v", err)
	}

	// Empty content
	invalidUser1 := conversation.EntryMessage{
		Role:    conversation.EntryRoleUser,
		Content: []conversation.EntryPart{},
	}
	if err := conversation.ValidateEntryMessage(invalidUser1); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for empty content, got %v", err)
	}

	// Empty text
	invalidUser2 := conversation.EntryMessage{
		Role: conversation.EntryRoleUser,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("   ")},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidUser2); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for blank text, got %v", err)
	}

	// User with tool call
	tc := mustProjectCall(t, "search", "v1", "c1", `{"q":"test"}`)
	invalidUser3 := conversation.EntryMessage{
		Role: conversation.EntryRoleUser,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartToolCall, ToolCall: &tc},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidUser3); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for user with toolCall, got %v", err)
	}

	// User with stop reason
	invalidUser4 := conversation.EntryMessage{
		Role:       conversation.EntryRoleUser,
		StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("hello")},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidUser4); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for user with stopReason, got %v", err)
	}
}

func TestValidateEntryMessage_Assistant(t *testing.T) {
	setupTestPolicies(t)

	// Valid text-only assistant
	validAsst1 := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("Here is your answer")},
		},
	}
	if err := conversation.ValidateEntryMessage(validAsst1); err != nil {
		t.Fatalf("expected valid text assistant, got %v", err)
	}

	// Text assistant with toolUse stop reason -> invalid
	invalidAsst1 := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("Here is your answer")},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidAsst1); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for text assistant with toolUse stopReason, got %v", err)
	}

	// Valid assistant with tool call
	tc := mustProjectCall(t, "search", "v1", "call-1", `{"q":"test"}`)
	validAsst2 := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("Searching...")},
			{Type: conversation.EntryPartToolCall, ToolCall: &tc},
		},
	}
	if err := conversation.ValidateEntryMessage(validAsst2); err != nil {
		t.Fatalf("expected valid tool assistant, got %v", err)
	}

	// Tool assistant with normal stop reason -> invalid
	invalidAsst2 := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartToolCall, ToolCall: &tc},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidAsst2); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for tool assistant with normal stopReason, got %v", err)
	}

	// Duplicate call IDs in same assistant message -> invalid
	tcDup := mustProjectCall(t, "search", "v1", "call-1", `{"q":"test"}`)
	invalidAsst3 := conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartToolCall, ToolCall: &tc},
			{Type: conversation.EntryPartToolCall, ToolCall: &tcDup},
		},
	}
	if err := conversation.ValidateEntryMessage(invalidAsst3); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for duplicate tool call ID, got %v", err)
	}
}

func TestValidateEntryMessage_ToolResult(t *testing.T) {
	setupTestPolicies(t)

	sr := mustProjectResult(t, "search", "v1", `{"count":5}`)
	validResult := conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: "e_asst_1",
			CallID:      "call-1",
			ToolName:    "search",
			IsError:     false,
			Result:      sr,
		},
	}
	if err := conversation.ValidateEntryMessage(validResult); err != nil {
		t.Fatalf("expected valid toolResult, got %v", err)
	}

	// ToolResult with content parts -> invalid
	invalidResult1 := conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartText, Text: stringPtr("some text")},
		},
		ToolResult: validResult.ToolResult,
	}
	if err := conversation.ValidateEntryMessage(invalidResult1); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for toolResult with content, got %v", err)
	}

	// ToolResult missing fields
	invalidResult2 := conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: "",
			CallID:      "call-1",
			ToolName:    "search",
			Result:      sr,
		},
	}
	if err := conversation.ValidateEntryMessage(invalidResult2); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for missing CallEntryID, got %v", err)
	}
}

func TestValidateToolContinuation(t *testing.T) {
	setupTestPolicies(t)
	now := time.Now().UTC()

	// 1. Root must be user
	err := conversation.ValidateToolContinuation(nil, conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopNormal,
		Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringPtr("hello")}},
	})
	if !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput when root is assistant, got %v", err)
	}

	rootUser := conversation.ConversationEntry{
		ID:             "e_u1",
		ConversationID: "c1",
		Kind:           conversation.EntryKindMessage,
		PayloadVersion: 1,
		Depth:          0,
		CreatedAt:      now,
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleUser,
			Content: []conversation.EntryPart{
				{Type: conversation.EntryPartText, Text: stringPtr("Search questions")},
			},
		},
	}

	// 2. User followed by orphan toolResult -> rejected
	err = conversation.ValidateToolContinuation([]conversation.ConversationEntry{rootUser}, conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: "e_none",
			CallID:      "c1",
			ToolName:    "search",
			Result:      mustProjectResult(t, "search", "v1", `{"count":1}`),
		},
	})
	if !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for orphan toolResult, got %v", err)
	}

	// 3. User followed by assistant with 2 tool calls
	tc1 := mustProjectCall(t, "search", "v1", "c1", `{"q":"test1"}`)
	tc2 := mustProjectCall(t, "calc", "v1", "c2", `{"expr":"1+1"}`)
	asstEntry := conversation.ConversationEntry{
		ID:             "e_a1",
		ConversationID: "c1",
		ParentID:       &rootUser.ID,
		Kind:           conversation.EntryKindMessage,
		PayloadVersion: 1,
		Depth:          1,
		CreatedAt:      now,
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopTools,
			Content: []conversation.EntryPart{
				{Type: conversation.EntryPartText, Text: stringPtr("Let me check")},
				{Type: conversation.EntryPartToolCall, ToolCall: &tc1},
				{Type: conversation.EntryPartToolCall, ToolCall: &tc2},
			},
		},
	}
	path := []conversation.ConversationEntry{rootUser, asstEntry}

	// Cannot cross over pending tool group with user message
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role:    conversation.EntryRoleUser,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringPtr("next question")}},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict when crossing pending tool group with user, got %v", err)
	}

	// Out of order: giving c2 before c1 -> rejected
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: asstEntry.ID,
			CallID:      "c2",
			ToolName:    "calc",
			Result:      mustProjectResult(t, "calc", "v1", `{"val":"42"}`),
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict for out of order result, got %v", err)
	}

	// Correct first tool result c1
	r1Entry := conversation.ConversationEntry{
		ID:             "e_r1",
		ConversationID: "c1",
		ParentID:       &asstEntry.ID,
		Kind:           conversation.EntryKindMessage,
		PayloadVersion: 1,
		Depth:          2,
		CreatedAt:      now,
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: asstEntry.ID,
				CallID:      "c1",
				ToolName:    "search",
				Result:      mustProjectResult(t, "search", "v1", `{"count":1}`),
			},
		},
	}
	path = append(path, r1Entry)

	// Still pending: need c2. Repeating c1 -> rejected
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: asstEntry.ID,
			CallID:      "c1",
			ToolName:    "search",
			Result:      mustProjectResult(t, "search", "v1", `{"count":1}`),
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict for repeating c1, got %v", err)
	}

	// Correct second tool result c2
	r2Entry := conversation.ConversationEntry{
		ID:             "e_r2",
		ConversationID: "c1",
		ParentID:       &r1Entry.ID,
		Kind:           conversation.EntryKindMessage,
		PayloadVersion: 1,
		Depth:          3,
		CreatedAt:      now,
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: asstEntry.ID,
				CallID:      "c2",
				ToolName:    "calc",
				Result:      mustProjectResult(t, "calc", "v1", `{"val":"42"}`),
			},
		},
	}
	path = append(path, r2Entry)

	// Group completed: extra tool result -> rejected
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: asstEntry.ID,
			CallID:      "c3",
			ToolName:    "calc",
			Result:      mustProjectResult(t, "calc", "v1", `{"val":"extra"}`),
		},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict for toolResult after group closed, got %v", err)
	}

	// Assistant can now respond
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopNormal,
		Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringPtr("Summary of results")}},
	})
	if err != nil {
		t.Fatalf("expected valid assistant following closed tool group, got %v", err)
	}

	// Duplicate call ID in subsequent assistant -> rejected
	tcDup := mustProjectCall(t, "search", "v1", "c1", `{"q":"dup"}`)
	err = conversation.ValidateToolContinuation(path, conversation.EntryMessage{
		Role:       conversation.EntryRoleAssistant,
		StopReason: conversation.EntryStopTools,
		Content:    []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &tcDup}},
	})
	if !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("expected ErrConflict for duplicate call ID in path, got %v", err)
	}
}

func TestIsClosedBranchPoint(t *testing.T) {
	setupTestPolicies(t)
	now := time.Now().UTC()
	u1 := conversation.ConversationEntry{
		ID:      "e_u1",
		Message: conversation.EntryMessage{Role: conversation.EntryRoleUser},
	}
	if !conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1}) {
		t.Fatal("user entry must be a valid branch point")
	}

	aPlain := conversation.ConversationEntry{
		ID: "e_a1",
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopNormal,
			Content:    []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringPtr("ok")}},
		},
	}
	if !conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1, aPlain}) {
		t.Fatal("plain assistant without tools must be a valid branch point")
	}

	tc1 := mustProjectCall(t, "tool1", "v1", "c1", `{"arg":"1"}`)
	tc2 := mustProjectCall(t, "tool2", "v1", "c2", `{"arg":"2"}`)
	aTools := conversation.ConversationEntry{
		ID: "e_a2",
		Message: conversation.EntryMessage{
			Role:       conversation.EntryRoleAssistant,
			StopReason: conversation.EntryStopTools,
			Content: []conversation.EntryPart{
				{Type: conversation.EntryPartToolCall, ToolCall: &tc1},
				{Type: conversation.EntryPartToolCall, ToolCall: &tc2},
			},
		},
	}
	if conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1, aTools}) {
		t.Fatal("assistant with pending tool calls cannot be a branch point")
	}

	r1 := conversation.ConversationEntry{
		ID: "e_r1",
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: aTools.ID,
				CallID:      "c1",
				ToolName:    "tool1",
				Result:      mustProjectResult(t, "tool1", "v1", `{"res":"done"}`),
			},
		},
	}
	if conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1, aTools, r1}) {
		t.Fatal("partial tool results cannot be a branch point")
	}

	// Corrupted result: wrong tool name -> IsClosedBranchPoint MUST be false!
	r2Corrupt := conversation.ConversationEntry{
		ID: "e_r2_bad",
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: aTools.ID,
				CallID:      "c2",
				ToolName:    "WRONG_TOOL",
				Result:      mustProjectResult(t, "tool2", "v1", `{"res":"done"}`),
			},
		},
	}
	if conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1, aTools, r1, r2Corrupt}) {
		t.Fatal("corrupted tool result must NOT be considered a closed branch point")
	}

	// Valid closed group
	r2 := conversation.ConversationEntry{
		ID: "e_r2",
		Message: conversation.EntryMessage{
			Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{
				CallEntryID: aTools.ID,
				CallID:      "c2",
				ToolName:    "tool2",
				Result:      mustProjectResult(t, "tool2", "v1", `{"res":"done"}`),
			},
		},
	}
	if !conversation.IsClosedBranchPoint([]conversation.ConversationEntry{u1, aTools, r1, r2}) {
		t.Fatal("final tool result of completed group must be a valid branch point")
	}
	_ = now
}

func TestPolicyDeepCopyAndImmutability(t *testing.T) {
	conversation.ClearToolPolicies()

	argRules := []conversation.ProjectionFieldRule{
		{SourceKey: "q", StoredKey: "q", MaxBytes: 20},
	}
	resRules := []conversation.ProjectionFieldRule{
		{SourceKey: "ans", StoredKey: "ans", MaxBytes: 50},
	}
	policy := conversation.ToolPersistencePolicy{
		ToolName:       "immutable_tool",
		Version:        "v1",
		ArgumentFields: argRules,
		ResultFields:   resRules,
	}

	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatalf("failed to register policy: %v", err)
	}

	// 1. Mutate original slice passed into RegisterToolPolicy
	argRules[0].SourceKey = "authorization"
	argRules[0].StoredKey = "cookie"

	// Fetch from registry, verify it was NOT mutated!
	retrieved, ok := conversation.GetToolPolicy("immutable_tool", "v1")
	if !ok {
		t.Fatal("policy not found")
	}
	if retrieved.ArgumentFields[0].SourceKey != "q" || retrieved.ArgumentFields[0].StoredKey != "q" {
		t.Fatalf("registry policy was mutated via original slice! Got: %+v", retrieved.ArgumentFields[0])
	}

	// 2. Mutate slice returned by GetToolPolicy
	retrieved.ArgumentFields[0].StoredKey = "tampered"
	retrieved2, ok := conversation.GetToolPolicy("immutable_tool", "v1")
	if !ok {
		t.Fatal("policy not found")
	}
	if retrieved2.ArgumentFields[0].StoredKey != "q" {
		t.Fatalf("registry policy was mutated via retrieved slice! Got: %+v", retrieved2.ArgumentFields[0])
	}
}

func TestSecurity_CannotBypassSafeProjection(t *testing.T) {
	setupTestPolicies(t)

	// 1. Unregistered tool call fails ValidateEntryMessage
	unregisteredJSON := []byte(`{
		"role": "assistant",
		"stopReason": "toolUse",
		"content": [
			{
				"type": "toolCall",
				"toolCall": {
					"id": "c1",
					"name": "unregistered_tool",
					"policyVersion": "v1",
					"fields": [{"name": "q", "value": "test"}]
				}
			}
		]
	}`)
	var unregMsg conversation.EntryMessage
	if err := json.Unmarshal(unregisteredJSON, &unregMsg); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateEntryMessage(unregMsg); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unregistered tool, got %v", err)
	}

	// 2. Sensitive key 'Authorization' in toolCall fails ValidateEntryMessage
	sensitiveJSON := []byte(`{
		"role": "assistant",
		"stopReason": "toolUse",
		"content": [
			{
				"type": "toolCall",
				"toolCall": {
					"id": "c1",
					"name": "search",
					"policyVersion": "v1",
					"fields": [{"name": "Authorization", "value": "Bearer secret"}]
				}
			}
		]
	}`)
	var sensMsg conversation.EntryMessage
	if err := json.Unmarshal(sensitiveJSON, &sensMsg); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateEntryMessage(sensMsg); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for sensitive field in toolCall, got %v", err)
	}

	// 3. Unapproved field name in toolCall fails ValidateEntryMessage
	unapprovedJSON := []byte(`{
		"role": "assistant",
		"stopReason": "toolUse",
		"content": [
			{
				"type": "toolCall",
				"toolCall": {
					"id": "c1",
					"name": "search",
					"policyVersion": "v1",
					"fields": [{"name": "unapproved_field", "value": "val"}]
				}
			}
		]
	}`)
	var unappMsg conversation.EntryMessage
	if err := json.Unmarshal(unapprovedJSON, &unappMsg); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateEntryMessage(unappMsg); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unapproved field in toolCall, got %v", err)
	}
}

func TestToolProjection(t *testing.T) {
	conversation.ClearToolPolicies()

	// 1. Sensitive key in policy -> rejected
	sensitivePolicy := conversation.ToolPersistencePolicy{
		ToolName: "test_tool",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "api_key", StoredKey: "key", MaxBytes: 100},
		},
	}
	if err := conversation.RegisterToolPolicy(sensitivePolicy); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for sensitive key, got %v", err)
	}

	// 2. Valid policy registration
	validPolicy := conversation.ToolPersistencePolicy{
		ToolName: "search_tool",
		Version:  "v1",
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "query", StoredKey: "q", MaxBytes: 20},
			{SourceKey: "limit", StoredKey: "lim", MaxBytes: 10},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "matches", StoredKey: "matchCount", MaxBytes: 10},
			{SourceKey: "summary", StoredKey: "summary", MaxBytes: 15},
		},
		MaxPreviewBytes: 30,
	}
	if err := conversation.RegisterToolPolicy(validPolicy); err != nil {
		t.Fatalf("failed to register policy: %v", err)
	}

	// 3. Unknown policy -> rejected
	unknownPolicy := conversation.ToolPersistencePolicy{ToolName: "unknown", Version: "v1"}
	_, err := conversation.ProjectSafeToolCall(unknownPolicy, "call-1", json.RawMessage(`{}`))
	if !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for unknown policy, got %v", err)
	}

	// 4. Argument projection: unapproved fields dropped, declared fields extracted
	rawArgs := json.RawMessage(`{
		"query": "binary search",
		"limit": 10,
		"unapproved_secret_token": "ignore_me",
		"nested_obj": {"a": 1}
	}`)
	safeCall, err := conversation.ProjectSafeToolCall(validPolicy, "call-1", rawArgs)
	if err != nil {
		t.Fatalf("project call failed: %v", err)
	}
	if safeCall.ID() != "call-1" || safeCall.Name() != "search_tool" || safeCall.PolicyVersion() != "v1" {
		t.Fatalf("unexpected call metadata: %+v", safeCall)
	}
	fields := safeCall.Fields()
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Name != "q" || fields[0].Value != "binary search" {
		t.Fatalf("unexpected field 0: %+v", fields[0])
	}
	if fields[1].Name != "lim" || fields[1].Value != "10" {
		t.Fatalf("unexpected field 1: %+v", fields[1])
	}

	// 5. Argument field exceeds MaxBytes -> Capacity error
	longArgs := json.RawMessage(`{"query": "this is a very long query that exceeds twenty bytes"}`)
	_, err = conversation.ProjectSafeToolCall(validPolicy, "call-2", longArgs)
	if !errors.Is(err, conversation.ErrEntryCapacity) {
		t.Fatalf("expected ErrEntryCapacity for long argument, got %v", err)
	}

	// 6. Result projection: fields extracted, UTF-8 truncation
	rawResult := json.RawMessage(`{
		"matches": 5,
		"summary": "This is a summary text that definitely exceeds 15 bytes",
		"ignored_raw_data": [1, 2, 3]
	}`)
	safeRes, err := conversation.ProjectSafeToolResult(validPolicy, rawResult)
	if err != nil {
		t.Fatalf("project result failed: %v", err)
	}
	if !safeRes.Truncated() {
		t.Fatalf("expected truncated to be true")
	}
	resFields := safeRes.Fields()
	if len(resFields) != 2 {
		t.Fatalf("expected 2 result fields, got %d", len(resFields))
	}
	if resFields[0].Name != "matchCount" || resFields[0].Value != "5" {
		t.Fatalf("unexpected matchCount: %+v", resFields[0])
	}
	if len(resFields[1].Value) > 15 {
		t.Fatalf("expected summary to be <= 15 bytes, got %d", len(resFields[1].Value))
	}
	if safeRes.Preview() == "" || len(safeRes.Preview()) > 30 {
		t.Fatalf("expected preview to be bounded <= 30 bytes, got %q", safeRes.Preview())
	}
}

func TestSafeSerialization(t *testing.T) {
	setupTestPolicies(t)

	tc := mustProjectCall(t, "search", "v1", "c1", `{"q":"hello"}`)
	bytes, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(bytes), "{}") {
		t.Fatalf("marshal produced empty object: %s", string(bytes))
	}

	var tc2 conversation.SafeToolCall
	if err := json.Unmarshal(bytes, &tc2); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if tc2.ID() != tc.ID() || tc2.Name() != tc.Name() || len(tc2.Fields()) != 1 {
		t.Fatalf("unmarshaled tc does not match: %+v", tc2)
	}

	// Defensive copy
	fields := tc2.Fields()
	fields[0].Value = "tampered"
	if tc2.Fields()[0].Value == "tampered" {
		t.Fatalf("Fields() must return a defensive copy")
	}
}
