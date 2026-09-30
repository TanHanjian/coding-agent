package conversation_test

import (
	"errors"
	"testing"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func pathValidationFixtures(t *testing.T) (user, plain, calls, first, second conversation.ConversationEntry) {
	t.Helper()
	setupTestPolicies(t)
	user = conversation.ConversationEntry{ID: "user", Message: conversation.EntryMessage{Role: conversation.EntryRoleUser}}
	plain = conversation.ConversationEntry{ID: "plain", Message: conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: stringPtr("done")}},
	}}
	c1 := mustProjectCall(t, "search", "v1", "call-1", `{"q":"one"}`)
	c2 := mustProjectCall(t, "calc", "v1", "call-2", `{"expr":"1+1"}`)
	calls = conversation.ConversationEntry{ID: "calls", Message: conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{
			{Type: conversation.EntryPartToolCall, ToolCall: &c1},
			{Type: conversation.EntryPartToolCall, ToolCall: &c2},
		},
	}}
	first = conversation.ConversationEntry{ID: "first", Message: conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult, ToolResult: &conversation.EntryToolResult{
			CallEntryID: calls.ID, CallID: c1.ID(), ToolName: c1.Name(),
			Result: mustProjectResult(t, "search", "v1", `{"count":1}`),
		},
	}}
	second = conversation.ConversationEntry{ID: "second", Message: conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult, ToolResult: &conversation.EntryToolResult{
			CallEntryID: calls.ID, CallID: c2.ID(), ToolName: c2.Name(),
			Result: mustProjectResult(t, "calc", "v1", `{"val":"2"}`),
		},
	}}
	return
}

func TestPathValidationRejectsCorruptHistory(t *testing.T) {
	u, plain, calls, r1, r2 := pathValidationFixtures(t)
	wrongEntry := r1.Clone()
	wrongEntry.Message.ToolResult.CallEntryID = "other-assistant"
	wrongName := r1.Clone()
	wrongName.Message.ToolResult.ToolName = "calc"
	missingResult := r1.Clone()
	missingResult.Message.ToolResult = nil
	duplicateCalls := calls.Clone()
	duplicateCalls.Message.Content[1] = duplicateCalls.Message.Content[0]
	missingCall := calls.Clone()
	missingCall.Message.Content[0].ToolCall = nil
	emptyCall := calls.Clone()
	emptyCall.Message.Content[0].ToolCall = &conversation.SafeToolCall{}
	wrongStop := calls.Clone()
	wrongStop.Message.StopReason = conversation.EntryStopNormal
	undeclaredTools := plain.Clone()
	undeclaredTools.Message.StopReason = conversation.EntryStopTools
	reusedCalls := calls.Clone()
	reusedCalls.ID = "later-assistant"

	cases := map[string][]conversation.ConversationEntry{
		"assistant root":                 {plain},
		"tool root":                      {r1},
		"unknown role":                   {u, {Message: conversation.EntryMessage{Role: "unknown"}}},
		"orphan after user":              {u, r1},
		"orphan after plain assistant":   {u, plain, r1},
		"swapped results":                {u, calls, r2, r1},
		"extra result":                   {u, calls, r1, r2, r2},
		"repeated result":                {u, calls, r1, r1},
		"wrong assistant identity":       {u, calls, wrongEntry},
		"wrong tool name":                {u, calls, wrongName},
		"missing result":                 {u, calls, missingResult},
		"user crosses pending":           {u, calls, u},
		"text assistant crosses pending": {u, calls, r1, plain},
		"tool assistant crosses pending": {u, calls, r1, calls},
		"duplicate local call IDs":       {u, duplicateCalls},
		"duplicate historical call IDs":  {u, calls, r1, r2, reusedCalls},
		"missing declared call":          {u, missingCall},
		"empty declared identity":        {u, emptyCall},
		"wrong call stop reason":         {u, wrongStop},
		"tool stop without declarations": {u, undeclaredTools},
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			// A superficially valid suffix must never hide earlier corruption.
			for _, suffix := range [][]conversation.ConversationEntry{nil, {u}, {plain}, {u, calls, r1, r2}} {
				full := append(append([]conversation.ConversationEntry(nil), path...), suffix...)
				if err := conversation.ValidatePathConsistency(full); !errors.Is(err, conversation.ErrEntryCorrupt) {
					t.Errorf("ValidatePathConsistency (suffix length %d): want ErrEntryCorrupt, got %v", len(suffix), err)
				}
				if conversation.IsClosedBranchPoint(full) {
					t.Errorf("IsClosedBranchPoint accepted corrupt history (suffix length %d)", len(suffix))
				}
				if err := conversation.ValidateToolContinuation(full, plain.Message); !errors.Is(err, conversation.ErrEntryCorrupt) {
					t.Errorf("ValidateToolContinuation (suffix length %d): want ErrEntryCorrupt, got %v", len(suffix), err)
				}
			}
		})
	}
}

func TestPathValidationPendingAndMinimalFixtures(t *testing.T) {
	u, plain, calls, r1, r2 := pathValidationFixtures(t)
	minimalAssistant := conversation.ConversationEntry{ID: "minimal", Message: conversation.EntryMessage{Role: conversation.EntryRoleAssistant}}
	cases := []struct {
		name   string
		path   []conversation.ConversationEntry
		closed bool
	}{
		{"empty", nil, true},
		{"minimal user", []conversation.ConversationEntry{u}, true},
		{"minimal assistant", []conversation.ConversationEntry{u, minimalAssistant}, true},
		{"plain assistant", []conversation.ConversationEntry{u, plain}, true},
		{"pending all", []conversation.ConversationEntry{u, calls}, false},
		{"pending one", []conversation.ConversationEntry{u, calls, r1}, false},
		{"completed", []conversation.ConversationEntry{u, calls, r1, r2}, true},
		{"completed then user", []conversation.ConversationEntry{u, calls, r1, r2, u}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := conversation.ValidatePathConsistency(tc.path); err != nil {
				t.Fatalf("valid history rejected: %v", err)
			}
			if got := conversation.IsClosedBranchPoint(tc.path); got != tc.closed {
				t.Fatalf("closed = %v, want %v", got, tc.closed)
			}
		})
	}
	if err := conversation.ValidateToolContinuation([]conversation.ConversationEntry{u, calls}, r1.Message); err != nil {
		t.Fatalf("first pending result rejected: %v", err)
	}
	if err := conversation.ValidateToolContinuation([]conversation.ConversationEntry{u, calls, r1}, r2.Message); err != nil {
		t.Fatalf("second pending result rejected: %v", err)
	}

	// A second group with fresh identities is valid, including a pending leaf.
	freshCall := mustProjectCall(t, "search", "v1", "call-3", `{"q":"three"}`)
	freshAssistant := calls.Clone()
	freshAssistant.ID = "fresh-assistant"
	freshAssistant.Message.Content = []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &freshCall}}
	freshResult := r1.Clone()
	freshResult.Message.ToolResult.CallEntryID = freshAssistant.ID
	freshResult.Message.ToolResult.CallID = freshCall.ID()
	completed := []conversation.ConversationEntry{u, calls, r1, r2, plain, u}
	if err := conversation.ValidateToolContinuation(completed, freshAssistant.Message); err != nil {
		t.Fatalf("fresh call rejected: %v", err)
	}
	pending := append(completed, freshAssistant)
	if err := conversation.ValidatePathConsistency(pending); err != nil {
		t.Fatalf("second pending group rejected: %v", err)
	}
	if conversation.IsClosedBranchPoint(pending) {
		t.Fatal("second pending group considered closed")
	}
	if err := conversation.ValidateToolContinuation(pending, freshResult.Message); err != nil {
		t.Fatalf("second group's result rejected: %v", err)
	}
	closed := append(pending, freshResult)
	if err := conversation.ValidatePathConsistency(closed); err != nil {
		t.Fatalf("multiple completed groups rejected: %v", err)
	}
	if !conversation.IsClosedBranchPoint(closed) {
		t.Fatal("multiple completed groups not considered closed")
	}
}

func TestPathValidationCallIDsArePathScoped(t *testing.T) {
	u, plain, calls, r1, r2 := pathValidationFixtures(t)
	parent := []conversation.ConversationEntry{u, plain}
	// Siblings may independently declare the same IDs; validation has no global state.
	for _, id := range []conversation.EntryID{"sibling-left", "sibling-right"} {
		sibling := calls.Clone()
		sibling.ID = id
		if err := conversation.ValidateToolContinuation(parent, sibling.Message); err != nil {
			t.Fatalf("sibling continuation rejected: %v", err)
		}
		path := append(append([]conversation.ConversationEntry(nil), parent...), sibling)
		if err := conversation.ValidatePathConsistency(path); err != nil {
			t.Fatalf("sibling history rejected: %v", err)
		}
	}
	completed := []conversation.ConversationEntry{u, calls, r1, r2}
	if err := conversation.ValidateToolContinuation(completed, calls.Message); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("reused path ID: want ErrConflict, got %v", err)
	}
	wrongOwner := r1.Clone()
	wrongOwner.Message.ToolResult.CallEntryID = "other-assistant"
	wrongName := r1.Clone()
	wrongName.Message.ToolResult.ToolName = "calc"
	wrongName.Message.ToolResult.Result = r2.Message.ToolResult.Result.Clone()
	for _, next := range []conversation.EntryMessage{plain.Message, r2.Message, wrongOwner.Message, wrongName.Message} {
		if err := conversation.ValidateToolContinuation([]conversation.ConversationEntry{u, calls}, next); !errors.Is(err, domainerr.ErrConflict) {
			t.Fatalf("invalid pending continuation: want ErrConflict, got %v", err)
		}
	}
	if err := conversation.ValidateToolContinuation(completed, r2.Message); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("extra continuation: want ErrConflict, got %v", err)
	}
}
