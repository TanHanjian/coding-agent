package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// Compile-time seams only: this fixture deliberately provides no persistence
// behavior. Legacy TurnStore stubs need not pretend to implement new Run writes.
type generationContractFixture struct {
	TurnStore
}

func (*generationContractFixture) MarkRunning(context.Context, agentrun.RunID) (agentrun.Run, error) {
	panic("contract fixture must not execute")
}

func (*generationContractFixture) CommitRunMessages(context.Context, CommitRunMessagesInput) (CommitRunMessagesResult, error) {
	panic("contract fixture must not execute")
}

type recorderContractFixture struct{}

func (*recorderContractFixture) RecordAssistant(context.Context, conversation.EntryMessage) error {
	panic("contract fixture must not execute")
}

func (*recorderContractFixture) RecordToolResults(context.Context, []conversation.EntryMessage) error {
	panic("contract fixture must not execute")
}

type recoveryContractFixture struct{}

func (*recoveryContractFixture) RecoverInterrupted(context.Context) (RecoveryResult, error) {
	panic("contract fixture must not execute")
}

var (
	_ GenerationStore = (*generationContractFixture)(nil)
	_ TurnStore       = (*generationContractFixture)(nil)
	_ RunRecorder     = (*recorderContractFixture)(nil)
	_ RecoveryStore   = (*recoveryContractFixture)(nil)
)

func runContractFixture() agentrun.Run {
	base, final := conversation.EntryID("e_base"), conversation.EntryID("e_final")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	started, finished := created.Add(time.Second), created.Add(2*time.Second)
	return agentrun.Run{
		ID: "r_contract", ConversationID: "c_contract", ClientMessageID: "client_contract",
		UserMessageID: "m_user", AssistantMessageID: "m_assistant",
		BaseEntryID: &base, BaseHeadVersion: 3, UserEntryID: "e_user",
		LastEntryID: final, LastClosedEntryID: final, FinalEntryID: &final, HeadVersion: 5,
		Status: agentrun.StatusCompleted, CreatedAt: created, StartedAt: &started, FinishedAt: &finished,
	}
}

func finalContractMessage() conversation.EntryMessage {
	text := "single complete model response"
	return conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}},
	}
}

func TestBeginTurnResultClonePreservesLegacyAndOwnsRun(t *testing.T) {
	run := runContractFixture()
	original := BeginTurnResult{
		Conversation:     conversation.Conversation{ID: run.ConversationID},
		History:          []conversation.Message{{ID: "m_history", Content: "old history"}},
		UserMessage:      conversation.Message{ID: run.UserMessageID},
		AssistantMessage: conversation.Message{ID: run.AssistantMessageID},
		Run:              &run, Reused: true,
	}
	cp := original.Clone()
	if !reflect.DeepEqual(original, cp) {
		t.Fatal("Clone changed BeginTurnResult")
	}
	cp.History[0].Content = "changed"
	cp.Run.ID = "r_changed"
	*cp.Run.BaseEntryID, *cp.Run.FinalEntryID = "e_changed", "e_changed"
	*cp.Run.StartedAt, *cp.Run.FinishedAt = time.Time{}, time.Time{}
	if original.History[0].Content != "old history" || original.Run.ID != "r_contract" || *original.Run.BaseEntryID != "e_base" || *original.Run.FinalEntryID != "e_final" || original.Run.StartedAt.IsZero() || original.Run.FinishedAt.IsZero() {
		t.Fatal("result clone shares mutable storage")
	}
	legacy := BeginTurnResult{Reused: true, UserMessage: original.UserMessage, AssistantMessage: original.AssistantMessage}.Clone()
	if legacy.Run != nil || legacy.History != nil || !legacy.Reused || legacy.AssistantMessage.ID != "m_assistant" {
		t.Fatal("legacy result lost nil Run/History or Message identity")
	}
}

func TestCommitRunMessagesInputCloneOwnsMessageAndToken(t *testing.T) {
	leaf := conversation.EntryID("e_user")
	original := CommitRunMessagesInput{
		RunID: "r_contract", ExpectedHead: conversation.HeadToken{ActiveLeafID: &leaf, Version: 4},
		Messages: []conversation.EntryMessage{finalContractMessage()},
	}
	cp := original.Clone()
	if !reflect.DeepEqual(original, cp) {
		t.Fatal("Clone changed commit input")
	}
	*cp.ExpectedHead.ActiveLeafID = "e_changed"
	*cp.Messages[0].Content[0].Text = "changed"
	cp.Messages[0].Role = conversation.EntryRoleUser
	if *original.ExpectedHead.ActiveLeafID != "e_user" || *original.Messages[0].Content[0].Text != "single complete model response" || original.Messages[0].Role != conversation.EntryRoleAssistant {
		t.Fatal("commit input clone shares mutable storage")
	}
}

func TestCommitRunMessagesResultCloneOwnsEntryHeadAndRun(t *testing.T) {
	run := runContractFixture()
	parent, leaf := conversation.EntryID("e_result"), conversation.EntryID("e_final")
	original := CommitRunMessagesResult{
		Run:     run,
		Entries: []conversation.ConversationEntry{{ID: leaf, ParentID: &parent, Message: finalContractMessage()}},
		Head:    conversation.EntryHead{ConversationID: run.ConversationID, ActiveLeafID: &leaf, Version: 5},
	}
	cp := original.Clone()
	if !reflect.DeepEqual(original, cp) {
		t.Fatal("Clone changed commit result")
	}
	*cp.Run.BaseEntryID, *cp.Run.FinalEntryID = "e_changed", "e_changed"
	*cp.Run.StartedAt, *cp.Run.FinishedAt = time.Time{}, time.Time{}
	*cp.Head.ActiveLeafID, *cp.Entries[0].ParentID = "e_changed", "e_changed"
	*cp.Entries[0].Message.Content[0].Text = "changed"
	if *original.Run.BaseEntryID != "e_base" || *original.Run.FinalEntryID != "e_final" || original.Run.StartedAt.IsZero() || original.Run.FinishedAt.IsZero() || *original.Head.ActiveLeafID != "e_final" || *original.Entries[0].ParentID != "e_result" || *original.Entries[0].Message.Content[0].Text != "single complete model response" {
		t.Fatal("commit result clone shares mutable storage")
	}
}

func TestFinishAssistantInputCloneOwnsFinalResponseAndToken(t *testing.T) {
	leaf := conversation.EntryID("e_result")
	head := conversation.HeadToken{ActiveLeafID: &leaf, Version: 4}
	final := finalContractMessage()
	original := FinishAssistantInput{
		AssistantMessageID: "m_assistant", Status: conversation.MessageStatusCompleted,
		ExpectedHead: &head, FinalMessage: &final,
	}
	cp := original.Clone()
	if !reflect.DeepEqual(original, cp) {
		t.Fatal("Clone changed finish input")
	}
	cp.ExpectedHead.Version = 100
	*cp.ExpectedHead.ActiveLeafID = "e_changed"
	*cp.FinalMessage.Content[0].Text = "changed"
	if original.ExpectedHead.Version != 4 || *original.ExpectedHead.ActiveLeafID != "e_result" || *original.FinalMessage.Content[0].Text != "single complete model response" {
		t.Fatal("finish input clone shares mutable storage")
	}
}

func TestCommitClonesOwnNestedToolFacts(t *testing.T) {
	policy := conversation.ToolPersistencePolicy{
		ToolName: "contract_clone_tool", Version: "v1", MaxPreviewBytes: 128,
		ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "limit", StoredKey: "limit", MaxBytes: 8}},
		ResultFields:   []conversation.ProjectionFieldRule{{SourceKey: "count", StoredKey: "count", MaxBytes: 8}},
	}
	// Reuse only this identical test policy on repeated test runs. Clearing
	// the process-wide registry would invalidate other tests' policies.
	if registered, ok := conversation.GetToolPolicy(policy.ToolName, policy.Version); ok {
		if !reflect.DeepEqual(registered, policy) {
			t.Fatal("contract fixture policy unexpectedly changed")
		}
	} else if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	call, err := conversation.ProjectSafeToolCall(policy, "call_1", json.RawMessage(`{"limit":5}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := conversation.ProjectSafeToolResult(policy, json.RawMessage(`{"count":3}`))
	if err != nil {
		t.Fatal(err)
	}
	callMessage := conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartToolCall, ToolCall: &call}},
	}
	resultMessage := conversation.EntryMessage{
		Role: conversation.EntryRoleToolResult,
		ToolResult: &conversation.EntryToolResult{
			CallEntryID: "e_calls", CallID: "call_1", ToolName: policy.ToolName, Result: result,
		},
	}
	// These are separate operation fixtures, not a permitted mixed batch.
	for _, message := range []conversation.EntryMessage{callMessage, resultMessage} {
		before := message.Clone()
		input := CommitRunMessagesInput{Messages: []conversation.EntryMessage{message}}
		output := CommitRunMessagesResult{Entries: []conversation.ConversationEntry{{Message: message}}}
		inputCopy, outputCopy := input.Clone(), output.Clone()
		if !reflect.DeepEqual(input, inputCopy) || !reflect.DeepEqual(output, outputCopy) {
			t.Fatal("tool fact changed during Clone")
		}
		for _, copyMessage := range []conversation.EntryMessage{inputCopy.Messages[0], outputCopy.Entries[0].Message} {
			if copyMessage.ToolResult != nil {
				copyMessage.ToolResult.CallID = "changed"
				copyMessage.ToolResult.IsError = true
				copyMessage.ToolResult.Result = conversation.SafeToolResult{}
			} else {
				*copyMessage.Content[0].ToolCall = conversation.SafeToolCall{}
			}
		}
		if !reflect.DeepEqual(input.Messages[0], before) || !reflect.DeepEqual(output.Entries[0].Message, before) {
			t.Fatal("tool fact clone shares nested references")
		}
	}
}

func TestContractClonesPreserveNilAndEmpty(t *testing.T) {
	if cp := (CommitRunMessagesInput{}).Clone(); cp.Messages != nil || cp.ExpectedHead.ActiveLeafID != nil {
		t.Fatal("empty commit input gained references")
	}
	if cp := (CommitRunMessagesResult{}).Clone(); cp.Entries != nil || cp.Head.ActiveLeafID != nil {
		t.Fatal("empty commit result gained references")
	}
	if cp := (FinishAssistantInput{}).Clone(); cp.ExpectedHead != nil || cp.FinalMessage != nil {
		t.Fatal("legacy finish input gained references")
	}
	if cp := (BeginTurnResult{History: []conversation.Message{}}).Clone(); cp.History == nil {
		t.Fatal("empty history became nil")
	}
	if cp := (CommitRunMessagesInput{Messages: []conversation.EntryMessage{}}).Clone(); cp.Messages == nil {
		t.Fatal("empty messages became nil")
	}
	if cp := (CommitRunMessagesResult{Entries: []conversation.ConversationEntry{}}).Clone(); cp.Entries == nil {
		t.Fatal("empty entries became nil")
	}
}

func TestRequestAndBuilderShareExplicitRunRecorderSeam(t *testing.T) {
	recorder := &recorderContractFixture{}
	request := Request{RunID: "r_contract", Recorder: recorder}
	input := BuildInput{RunID: request.RunID, Recorder: request.Recorder}
	if input.RunID != request.RunID || input.Recorder != recorder {
		t.Fatal("Run identity or Recorder seam lost")
	}
	// This only checks the contract. Executor forwarding and required-Recorder
	// production enforcement are deliberately deferred to the wiring slice.
}
