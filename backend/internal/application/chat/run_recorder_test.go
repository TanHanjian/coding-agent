package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// ignoreEntryID drops the EntryID return value from RecordAssistant for tests
// that don't need to inspect it.
func ignoreEntryID(_ conversation.EntryID, err error) error {
	return err
}

// recorderStore controls the transaction boundary and checks every expected
// token against its committed state, rather than returning canned successes.
type recorderStore struct {
	TurnStore
	mu      sync.Mutex
	run     agentrun.Run
	inputs  []CommitRunMessagesInput
	outputs []CommitRunMessagesResult
	hook    func(context.Context, CommitRunMessagesInput) error
	active  int
	max     int
}

func newRecorderStore() *recorderStore {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return &recorderStore{run: agentrun.Run{
		ID: "r_recorder", ConversationID: "c_recorder", ClientMessageID: "client_recorder",
		UserMessageID: "m_user", AssistantMessageID: "m_assistant", UserEntryID: "e_user",
		LastEntryID: "e_user", LastClosedEntryID: "e_user", HeadVersion: 1,
		Status: agentrun.StatusRunning, CreatedAt: now, StartedAt: &now,
	}}
}

func (s *recorderStore) MarkRunning(context.Context, agentrun.RunID) (agentrun.Run, error) {
	panic("recorder must not start runs")
}

func (s *recorderStore) CommitRunMessages(ctx context.Context, input CommitRunMessagesInput) (CommitRunMessagesResult, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.max {
		s.max = s.active
	}
	s.inputs = append(s.inputs, input)
	hook := s.hook
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}()
	if hook != nil {
		if err := hook(ctx, input); err != nil {
			// A failed transaction can have prepared values; none are committed.
			return CommitRunMessagesResult{Run: agentrun.Run{HeadVersion: 999}, Head: conversation.EntryHead{Version: 999}}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return CommitRunMessagesResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.run.ActiveHeadToken()
	if err != nil || input.RunID != s.run.ID || !reflect.DeepEqual(input.ExpectedHead, token) {
		return CommitRunMessagesResult{}, fmt.Errorf("%w: stale recorder token", domainerr.ErrConflict)
	}
	next := s.run.Clone()
	entries := make([]conversation.ConversationEntry, len(input.Messages))
	for i, message := range input.Messages {
		if err := conversation.ValidateEntryMessage(message); err != nil {
			return CommitRunMessagesResult{}, err
		}
		if next.Status == agentrun.StatusRunning && message.Role != conversation.EntryRoleAssistant || next.Status == agentrun.StatusWaitingTool && message.Role != conversation.EntryRoleToolResult {
			return CommitRunMessagesResult{}, domainerr.ErrConflict
		}
		next.HeadVersion++
		id := conversation.EntryID(fmt.Sprintf("e_%d", next.HeadVersion))
		msg := message.Clone()
		entries[i] = conversation.ConversationEntry{ID: id, Message: &msg}
		next.LastEntryID = id
	}
	if next.Status == agentrun.StatusRunning {
		next.Status = agentrun.StatusWaitingTool
	} else {
		next.Status, next.LastClosedEntryID = agentrun.StatusRunning, next.LastEntryID
	}
	s.run = next.Clone()
	id := next.LastEntryID
	result := CommitRunMessagesResult{
		Run: next, Entries: entries,
		Head: conversation.EntryHead{ConversationID: next.ConversationID, ActiveLeafID: &id, Version: next.HeadVersion},
	}
	s.outputs = append(s.outputs, result)
	return result, nil
}

func recorderFacts(t *testing.T) (conversation.EntryMessage, []conversation.EntryMessage) {
	t.Helper()
	policy := conversation.ToolPersistencePolicy{
		ToolName: "recorder_test_tool", Version: "v1", MaxPreviewBytes: 128,
		ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "query", StoredKey: "query", MaxBytes: 64}},
		ResultFields:   []conversation.ProjectionFieldRule{{SourceKey: "summary", StoredKey: "summary", MaxBytes: 64}},
	}
	if existing, ok := conversation.GetToolPolicy(policy.ToolName, policy.Version); ok {
		if !reflect.DeepEqual(existing, policy) {
			t.Fatal("recorder tool policy changed")
		}
	} else if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	text := "complete assistant call response"
	message := conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}},
	}
	var results []conversation.EntryMessage
	for _, id := range []string{"call_a", "call_b"} {
		call, err := conversation.ProjectSafeToolCall(policy, id, json.RawMessage(`{"query":"approved"}`))
		if err != nil {
			t.Fatal(err)
		}
		result, err := conversation.ProjectSafeToolResult(policy, json.RawMessage(`{"summary":"approved"}`))
		if err != nil {
			t.Fatal(err)
		}
		message.Content = append(message.Content, conversation.EntryPart{Type: conversation.EntryPartToolCall, ToolCall: &call})
		results = append(results, conversation.EntryMessage{
			Role:       conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{CallID: id, ToolName: policy.ToolName, Result: result},
		})
	}
	return message, results
}

func recorderForTest(t *testing.T, store *recorderStore) *runRecorder {
	t.Helper()
	r, err := newRunRecorder(store, store.run)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitRecorderSignal[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for recorder")
		var zero T
		return zero
	}
}

func TestRunRecorderSerializesTokensAndOrdersResults(t *testing.T) {
	store := newRecorderStore()
	entered, release := make(chan struct{}), make(chan struct{})
	store.hook = func(_ context.Context, input CommitRunMessagesInput) error {
		if input.Messages[0].Role == conversation.EntryRoleAssistant {
			close(entered)
			<-release
		}
		return nil
	}
	r := recorderForTest(t, store)
	call, results := recorderFacts(t)
	assistantDone, resultsDone := make(chan error, 1), make(chan error, 1)
	go func() { assistantDone <- ignoreEntryID(r.RecordAssistant(context.Background(), call)) }()
	waitRecorderSignal(t, entered)
	results[0], results[1] = results[1], results[0]
	go func() { resultsDone <- r.RecordToolResults(context.Background(), results) }()
	close(release)
	if err := waitRecorderSignal(t, assistantDone); err != nil {
		t.Fatal(err)
	}
	if err := waitRecorderSignal(t, resultsDone); err != nil {
		t.Fatal(err)
	}
	if store.max != 1 || len(store.inputs) != 2 || store.inputs[0].ExpectedHead.Version != 1 || store.inputs[1].ExpectedHead.Version != 2 || *store.inputs[1].ExpectedHead.ActiveLeafID != "e_2" {
		t.Fatalf("commits were not serialized with fresh tokens: %+v", store.inputs)
	}
	for i, id := range []string{"call_a", "call_b"} {
		result := store.inputs[1].Messages[i].ToolResult
		if result.CallEntryID != "e_2" || result.CallID != id {
			t.Fatalf("result not controlled/ordered: %+v", result)
		}
		if results[i].ToolResult.CallEntryID != "" {
			t.Fatal("recorder modified caller result")
		}
	}
	token, final, err := r.finishSnapshot()
	if err != nil || final != nil || token.Version != 4 || *token.ActiveLeafID != "e_4" {
		t.Fatalf("missing-final snapshot = %+v, %+v, %v", token, final, err)
	}
}

func TestRunRecorderRetainsFailureWithoutReplayOrTokenAdvance(t *testing.T) {
	for _, stage := range []string{"assistant", "results"} {
		t.Run(stage, func(t *testing.T) {
			store := newRecorderStore()
			r := recorderForTest(t, store)
			call, results := recorderFacts(t)
			wantVersion, wantCalls := int64(1), 1
			if stage == "results" {
				if _, err := r.RecordAssistant(context.Background(), call); err != nil {
					t.Fatal(err)
				}
				wantVersion, wantCalls = 2, 2
			}
			failure := errors.New("controlled transaction failure")
			store.hook = func(context.Context, CommitRunMessagesInput) error { return failure }
			var err error
			if stage == "assistant" {
				_, err = r.RecordAssistant(context.Background(), call)
			} else {
				err = r.RecordToolResults(context.Background(), results)
			}
			if !errors.Is(err, failure) {
				t.Fatalf("record error = %v", err)
			}
			if _, err := r.RecordAssistant(context.Background(), finalContractMessage()); !errors.Is(err, failure) {
				t.Fatalf("ignored failure allowed success: %v", err)
			}
			if err := r.RecordToolResults(context.Background(), results); !errors.Is(err, failure) {
				t.Fatalf("failure was retried: %v", err)
			}
			token, final, err := r.finishSnapshot()
			if !errors.Is(err, failure) || token.Version != wantVersion || final != nil || len(store.inputs) != wantCalls {
				t.Fatalf("failure snapshot = %+v, %+v, %v; commits = %d", token, final, err, len(store.inputs))
			}
		})
	}
}

func TestRunRecorderRejectsMismatchedToolGroups(t *testing.T) {
	for _, variant := range []string{"missing", "extra", "duplicate", "unknown_call", "wrong_tool", "wrong_entry", "malformed", "unsafe_result", "pending_assistant"} {
		t.Run(variant, func(t *testing.T) {
			store := newRecorderStore()
			r := recorderForTest(t, store)
			call, results := recorderFacts(t)
			if _, err := r.RecordAssistant(context.Background(), call); err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "missing":
				results = results[:1]
			case "extra":
				results = append(results, results[0].Clone())
			case "duplicate":
				results[1] = results[0].Clone()
			case "unknown_call":
				results[0].ToolResult.CallID = "unknown"
			case "wrong_tool":
				results[0].ToolResult.ToolName = "unknown_tool"
			case "wrong_entry":
				results[0].ToolResult.CallEntryID = "e_other_assistant"
			case "malformed":
				results[0].ToolResult = nil
			case "unsafe_result":
				results[0].ToolResult.Result = conversation.SafeToolResult{}
			}
			var err error
			if variant == "pending_assistant" {
				_, err = r.RecordAssistant(context.Background(), finalContractMessage())
			} else {
				err = r.RecordToolResults(context.Background(), results)
			}
			if err == nil {
				t.Fatal("invalid group accepted")
			}
			token, final, snapshotErr := r.finishSnapshot()
			if snapshotErr != err || token.Version != 2 || final != nil || len(store.inputs) != 1 {
				t.Fatalf("rejected group advanced state or lost error: %+v, %+v, %v", token, final, snapshotErr)
			}
		})
	}
}

func TestRunRecorderClonesInputsOutputsAndKeepsFinalInMemory(t *testing.T) {
	store := newRecorderStore()
	r := recorderForTest(t, store)
	call, results := recorderFacts(t)
	original := call.Clone()
	if _, err := r.RecordAssistant(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	*call.Content[0].Text = "changed"
	*call.Content[1].ToolCall = conversation.SafeToolCall{}
	*store.outputs[0].Head.ActiveLeafID = "e_changed"
	store.outputs[0].Run.ID = "r_changed"
	*store.outputs[0].Run.StartedAt = time.Time{}
	if !reflect.DeepEqual(store.inputs[0].Messages[0], original) || r.run.StartedAt.IsZero() {
		t.Fatal("recorder shares caller/store nested data")
	}
	results[0].ToolResult.CallEntryID = "e_2" // A matching explicit identity is permitted.
	if err := r.RecordToolResults(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	originalResult := store.inputs[1].Messages[0].Clone()
	results[0].ToolResult.Result = conversation.SafeToolResult{}
	results[0].ToolResult.CallID = "changed"
	if !reflect.DeepEqual(store.inputs[1].Messages[0], originalResult) {
		t.Fatal("tool result input aliases store data")
	}
	for _, text := range []string{"earlier complete response", "latest complete response"} {
		message := finalContractMessage()
		*message.Content[0].Text = text
		if _, err := r.RecordAssistant(context.Background(), message); err != nil {
			t.Fatal(err)
		}
		*message.Content[0].Text = "mutated caller"
	}
	token, final, err := r.finishSnapshot()
	if err != nil || final == nil || *final.Content[0].Text != "latest complete response" || len(store.inputs) != 2 {
		t.Fatalf("final was not retained only in memory: %+v, %v", final, err)
	}
	*token.ActiveLeafID = "e_changed"
	*final.Content[0].Text = "mutated snapshot"
	again, againFinal, err := r.finishSnapshot()
	if err != nil || again.Version != 4 || *again.ActiveLeafID != "e_4" || *againFinal.Content[0].Text != "latest complete response" {
		t.Fatal("snapshot exposes mutable recorder storage")
	}
}

func TestRunRecorderSealWaitsForCommitAndRejectsLateRecords(t *testing.T) {
	store := newRecorderStore()
	entered, release := make(chan struct{}), make(chan struct{})
	store.hook = func(context.Context, CommitRunMessagesInput) error {
		close(entered)
		<-release
		return nil
	}
	r := recorderForTest(t, store)
	call, results := recorderFacts(t)
	done := make(chan error, 1)
	go func() { done <- ignoreEntryID(r.RecordAssistant(context.Background(), call)) }()
	waitRecorderSignal(t, entered)
	type snapshot struct {
		token conversation.HeadToken
		final *conversation.EntryMessage
		err   error
	}
	finished := make(chan snapshot, 1)
	go func() {
		token, final, err := r.finishSnapshot()
		finished <- snapshot{token, final, err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !r.sealed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("snapshot did not close admission")
		}
		runtime.Gosched()
	}
	select {
	case <-finished:
		t.Fatal("snapshot returned before commit completed")
	default:
	}
	close(release)
	if err := waitRecorderSignal(t, done); err != nil {
		t.Fatal(err)
	}
	got := waitRecorderSignal(t, finished)
	if got.err != nil || got.final != nil || got.token.Version != 2 || *got.token.ActiveLeafID != "e_2" {
		t.Fatalf("snapshot missed in-flight commit: %+v", got)
	}
	if err := r.RecordToolResults(context.Background(), results); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("late result accepted: %v", err)
	}
	if _, err := r.RecordAssistant(context.Background(), finalContractMessage()); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("late assistant accepted: %v", err)
	}
	if len(store.inputs) != 1 {
		t.Fatal("sealed recorder wrote late facts")
	}
}

func TestRunRecorderCancellationRejectsLateWrites(t *testing.T) {
	for _, stage := range []string{"call", "results", "final"} {
		t.Run(stage, func(t *testing.T) {
			store := newRecorderStore()
			r := recorderForTest(t, store)
			call, results := recorderFacts(t)
			wantVersion, wantCalls := int64(1), 0
			if stage == "results" {
				if _, err := r.RecordAssistant(context.Background(), call); err != nil {
					t.Fatal(err)
				}
				wantVersion, wantCalls = 2, 1
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch stage {
			case "results":
				err = r.RecordToolResults(ctx, results)
			case "final":
				_, err = r.RecordAssistant(ctx, finalContractMessage())
			default:
				_, err = r.RecordAssistant(ctx, call)
			}
			token, final, snapshotErr := r.finishSnapshot()
			if !errors.Is(err, context.Canceled) || !errors.Is(snapshotErr, context.Canceled) || token.Version != wantVersion || final != nil || len(store.inputs) != wantCalls {
				t.Fatalf("cancelled recording changed state: %+v, %+v, %v, %v", token, final, err, snapshotErr)
			}
		})
	}
}

func TestRunRecorderCancellationWhileWaitingForCommit(t *testing.T) {
	store := newRecorderStore()
	entered, release := make(chan struct{}), make(chan struct{})
	store.hook = func(context.Context, CommitRunMessagesInput) error {
		close(entered)
		<-release
		return nil
	}
	r := recorderForTest(t, store)
	call, results := recorderFacts(t)
	assistantDone, resultsDone := make(chan error, 1), make(chan error, 1)
	go func() { assistantDone <- ignoreEntryID(r.RecordAssistant(context.Background(), call)) }()
	waitRecorderSignal(t, entered)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { resultsDone <- r.RecordToolResults(ctx, results) }()
	cancel()
	close(release)
	if err := waitRecorderSignal(t, assistantDone); err != nil {
		t.Fatal(err)
	}
	if err := waitRecorderSignal(t, resultsDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancelled result = %v", err)
	}
	token, _, err := r.finishSnapshot()
	if !errors.Is(err, context.Canceled) || token.Version != 2 || len(store.inputs) != 1 {
		t.Fatal("cancelled waiter committed late results")
	}
}

func TestRunRecorderCancellationDuringCommitKeepsCommittedToken(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed_%t", committed), func(t *testing.T) {
			store := newRecorderStore()
			entered, release := make(chan struct{}), make(chan struct{})
			store.hook = func(ctx context.Context, _ CommitRunMessagesInput) error {
				close(entered)
				<-release
				if !committed {
					return ctx.Err()
				}
				return nil
			}
			r := recorderForTest(t, store)
			call, _ := recorderFacts(t)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- ignoreEntryID(r.RecordAssistant(ctx, call)) }()
			waitRecorderSignal(t, entered)
			if !committed {
				cancel()
			}
			close(release)
			err := waitRecorderSignal(t, done)
			// Cancellation after a successful Store return cannot undo the fact.
			cancel()
			token, final, snapshotErr := r.finishSnapshot()
			if committed {
				if err != nil || snapshotErr != nil || token.Version != 2 || *token.ActiveLeafID != "e_2" || final != nil {
					t.Fatalf("committed fact lost on cancellation: %+v, %v, %v", token, err, snapshotErr)
				}
			} else if !errors.Is(err, context.Canceled) || !errors.Is(snapshotErr, context.Canceled) || token.Version != 1 || final != nil {
				t.Fatalf("cancelled transaction advanced recorder: %+v, %v, %v", token, err, snapshotErr)
			}
		})
	}
}

func TestRunRecorderLaterToolRoundClearsEarlierFinal(t *testing.T) {
	store := newRecorderStore()
	r := recorderForTest(t, store)
	if _, err := r.RecordAssistant(context.Background(), finalContractMessage()); err != nil {
		t.Fatal(err)
	}
	call, results := recorderFacts(t)
	if _, err := r.RecordAssistant(context.Background(), call); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordToolResults(context.Background(), results); err != nil {
		t.Fatal(err)
	}
	token, final, err := r.finishSnapshot()
	if err != nil || final != nil || token.Version != 4 {
		t.Fatalf("earlier response survived later model round: %+v, %+v, %v", token, final, err)
	}
}

func TestRunRecorderConstructorAndMessageValidation(t *testing.T) {
	store := newRecorderStore()
	if _, err := newRunRecorder(nil, store.run); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("nil store accepted: %v", err)
	}
	if _, err := newRunRecorder(store, agentrun.Run{}); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("invalid run accepted: %v", err)
	}
	waiting := store.run.Clone()
	waiting.Status, waiting.LastEntryID, waiting.HeadVersion = agentrun.StatusWaitingTool, "e_calls", 2
	if _, err := newRunRecorder(store, waiting); !errors.Is(err, domainerr.ErrConflict) {
		t.Fatalf("resumed tool group accepted: %v", err)
	}
	pending := store.run.Clone()
	pending.Status, pending.StartedAt = agentrun.StatusPending, nil
	if _, err := newRunRecorder(store, pending); err != nil {
		t.Fatalf("pending service startup rejected: %v", err)
	}
	for _, variant := range []string{"wrong_role", "incomplete_final", "unsafe_call", "orphan_result"} {
		t.Run(variant, func(t *testing.T) {
			s := newRecorderStore()
			r := recorderForTest(t, s)
			call, results := recorderFacts(t)
			message := finalContractMessage()
			var err error
			switch variant {
			case "wrong_role":
				message.Role = conversation.EntryRoleUser
			case "incomplete_final":
				message.StopReason = conversation.EntryStopTools
			case "unsafe_call":
				message = call
				*message.Content[1].ToolCall = conversation.SafeToolCall{}
			}
			if variant == "orphan_result" {
				err = r.RecordToolResults(context.Background(), results)
			} else {
				_, err = r.RecordAssistant(context.Background(), message)
			}
			token, final, snapshotErr := r.finishSnapshot()
			if err == nil || snapshotErr != err || token.Version != 1 || final != nil || len(s.inputs) != 0 {
				t.Fatalf("invalid fact was accepted or failure lost: %+v, %+v, %v, %v", token, final, err, snapshotErr)
			}
		})
	}
}
