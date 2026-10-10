package chat

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// runRecorder serializes complete facts for one execution. It never replays
// commits: the first recording error stops further writes and survives sealing.
type runRecorder struct {
	mu     sync.Mutex
	sealed atomic.Bool
	store  GenerationStore
	run    agentrun.Run
	token  conversation.HeadToken
	callID conversation.EntryID
	calls  []conversation.DeclaredToolCall
	final  *conversation.EntryMessage
	err    error
}

var _ RunRecorder = (*runRecorder)(nil)

func newRunRecorder(store GenerationStore, run agentrun.Run) (*runRecorder, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: run recorder requires a generation store", domainerr.ErrInvalidInput)
	}
	token, err := run.ActiveHeadToken()
	if err != nil {
		return nil, err
	}
	// A pending Run is recorded before MarkRunning during service startup.
	// Waiting tool groups require their original declarations, not recovery.
	if run.Status != agentrun.StatusPending && run.Status != agentrun.StatusRunning {
		return nil, fmt.Errorf("%w: run recorder cannot resume a tool group", domainerr.ErrConflict)
	}
	return &runRecorder{store: store, run: run.Clone(), token: token.Clone()}, nil
}

func (r *runRecorder) RecordAssistant(ctx context.Context, message conversation.EntryMessage) (conversation.EntryID, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.recordable(ctx); err != nil {
		return "", err
	}
	message = message.Clone()
	if message.Role != conversation.EntryRoleAssistant {
		return "", r.fail(fmt.Errorf("%w: recorder requires an assistant message", domainerr.ErrInvalidInput))
	}
	if err := conversation.ValidateEntryMessage(message); err != nil {
		return "", r.fail(err)
	}
	if len(r.calls) != 0 {
		return "", r.fail(fmt.Errorf("%w: recorder has pending tool results", domainerr.ErrConflict))
	}
	var calls []conversation.DeclaredToolCall
	for _, part := range message.Content {
		if part.Type == conversation.EntryPartToolCall {
			calls = append(calls, conversation.DeclaredToolCall{ID: part.ToolCall.ID(), Name: part.ToolCall.Name()})
		}
	}
	if len(calls) == 0 {
		if err := ctx.Err(); err != nil {
			return "", r.fail(err)
		}
		r.final = &message
		return "", nil
	}
	if err := r.commit(ctx, []conversation.EntryMessage{message}); err != nil {
		return "", err
	}
	r.callID = r.run.LastEntryID
	r.calls = calls
	// A later model round supersedes any earlier no-call snapshot.
	r.final = nil
	return r.callID, nil
}

func (r *runRecorder) RecordToolResults(ctx context.Context, messages []conversation.EntryMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.recordable(ctx); err != nil {
		return err
	}
	if len(r.calls) == 0 {
		return r.fail(fmt.Errorf("%w: recorder has no pending tool group", domainerr.ErrConflict))
	}
	if len(messages) != len(r.calls) {
		return r.fail(fmt.Errorf("%w: recorder requires the complete tool result group", domainerr.ErrInvalidInput))
	}
	byID := make(map[string]conversation.EntryMessage, len(messages))
	for _, input := range messages {
		message := input.Clone()
		if message.Role != conversation.EntryRoleToolResult || message.ToolResult == nil {
			return r.fail(fmt.Errorf("%w: recorder requires independent tool results", domainerr.ErrInvalidInput))
		}
		result := message.ToolResult
		if result.CallEntryID != "" && result.CallEntryID != r.callID {
			return r.fail(fmt.Errorf("%w: tool result belongs to another assistant", domainerr.ErrConflict))
		}
		result.CallEntryID = r.callID
		if err := conversation.ValidateEntryMessage(message); err != nil {
			return r.fail(err)
		}
		if _, exists := byID[result.CallID]; exists {
			return r.fail(fmt.Errorf("%w: duplicate tool result", domainerr.ErrInvalidInput))
		}
		byID[result.CallID] = message
	}
	ordered := make([]conversation.EntryMessage, len(r.calls))
	for i, call := range r.calls {
		message, ok := byID[call.ID]
		if !ok || message.ToolResult.ToolName != call.Name {
			return r.fail(fmt.Errorf("%w: tool result does not match its declaration", domainerr.ErrConflict))
		}
		ordered[i] = message
	}
	if err := r.commit(ctx, ordered); err != nil {
		return err
	}
	r.callID, r.calls = "", nil
	return nil
}

// recordable is called under mu, including after waiting for another commit.
func (r *runRecorder) recordable(ctx context.Context) error {
	if r.sealed.Load() {
		return fmt.Errorf("%w: run recorder is sealed", domainerr.ErrConflict)
	}
	if r.err != nil {
		return r.err
	}
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	return nil
}

func (r *runRecorder) fail(err error) error {
	if r.err == nil {
		r.err = err
	}
	return r.err
}

// commit runs under mu. Only successful Store returns advance the token;
// cancellation after a committed transaction must not hide its committed head.
func (r *runRecorder) commit(ctx context.Context, messages []conversation.EntryMessage) error {
	if err := ctx.Err(); err != nil {
		return r.fail(err)
	}
	result, err := r.store.CommitRunMessages(ctx, (CommitRunMessagesInput{
		RunID: r.run.ID, ExpectedHead: r.token, Messages: messages,
	}).Clone())
	if err != nil {
		return r.fail(err)
	}
	r.run = result.Run.Clone()
	r.token = (conversation.HeadToken{ActiveLeafID: result.Head.ActiveLeafID, Version: result.Head.Version}).Clone()
	return nil
}

// finishSnapshot closes admission before waiting for the active serialized
// submission. Repeated snapshots are detached copies of the same sealed state.
// A missing final remains nil; the service decides the terminal outcome.
func (r *runRecorder) finishSnapshot() (conversation.HeadToken, *conversation.EntryMessage, error) {
	r.sealed.Store(true)
	r.mu.Lock()
	defer r.mu.Unlock()
	var final *conversation.EntryMessage
	if r.final != nil {
		message := r.final.Clone()
		final = &message
	}
	return r.token.Clone(), final, r.err
}
