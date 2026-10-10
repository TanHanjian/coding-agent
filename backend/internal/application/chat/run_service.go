package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// ErrRunUnavailable means a streaming Run cannot be resumed or finalized.
var ErrRunUnavailable = errors.New("generation unavailable")

type RunDependencies struct {
	Executor    Executor
	Registry    OwnedGenerationRegistry
	Hub         OwnedGenerationHub
	RunContexts RunContextFactory
	Store       GenerationStore
}

// RunService is opt-in until Graph control hooks and recovery are delivered.
type RunService struct {
	executor    Executor
	registry    OwnedGenerationRegistry
	hub         OwnedGenerationHub
	runContexts RunContextFactory
	store       GenerationStore
	startMu     sync.Mutex
}

func NewRunService(deps RunDependencies) (*RunService, error) {
	if deps.Executor == nil || deps.Registry == nil || deps.Hub == nil || deps.RunContexts == nil || deps.Store == nil {
		return nil, domainerr.ErrInvalidInput
	}
	return &RunService{executor: deps.Executor, registry: deps.Registry, hub: deps.Hub, runContexts: deps.RunContexts, store: deps.Store}, nil
}

func (s *RunService) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.ClientMessageID) == "" || strings.TrimSpace(input.Text) == "" {
		return StartResult{}, domainerr.ErrInvalidInput
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	turn, err := s.store.BeginTurn(ctx, BeginTurnInput(input))
	if err != nil {
		return StartResult{}, err
	}
	result := StartResult{ConversationID: turn.Conversation.ID, UserMessageID: turn.UserMessage.ID, AssistantMessageID: turn.AssistantMessage.ID}
	if turn.Reused {
		return result, nil
	}
	if turn.Run == nil {
		return StartResult{}, fmt.Errorf("%w: new turn has no Run", ErrRunUnavailable)
	}
	runctx, cancel := s.runContexts.New()
	invalidContext := runctx == nil || cancel == nil
	if cancel == nil {
		cancel = func() {}
	}
	registryLease, ok := s.registry.Claim(turn.AssistantMessage.ID, cancel)
	if !ok {
		cancel()
		return StartResult{}, domainerr.ErrConflict
	}
	hubLease, ok := s.hub.Claim(turn.AssistantMessage)
	if !ok {
		cancel()
		registryLease.Release()
		return StartResult{}, domainerr.ErrConflict
	}
	compensate := func(cause error) (StartResult, error) {
		cancel()
		defer registryLease.Release()
		defer hubLease.Release()
		token := conversation.HeadToken{ActiveLeafID: &turn.Run.LastEntryID, Version: turn.Run.HeadVersion}.Clone()
		finalizeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		code, text := runFailure("generation_start_failed")
		message, finishErr := s.store.FinishAssistant(finalizeCtx, FinishAssistantInput{AssistantMessageID: turn.AssistantMessage.ID, Status: conversation.MessageStatusFailed, ErrorCode: code, ErrorMessage: text, ExpectedHead: &token})
		if finishErr != nil {
			return StartResult{}, fmt.Errorf("%w: start compensation failed", ErrRunUnavailable)
		}
		hubLease.Complete(message)
		return StartResult{}, cause
	}
	if invalidContext {
		return compensate(domainerr.ErrInvalidInput)
	}
	recorder, err := newRunRecorder(s.store, *turn.Run)
	if err != nil {
		return compensate(err)
	}
	if _, err = s.store.MarkRunning(ctx, turn.Run.ID); err != nil {
		return compensate(err)
	}
	writer := newGenerationTextWriter(s.store, s.hub, turn.AssistantMessage.ID)
	sink := newBufferedTextSink(writer, turn.AssistantMessage.ID, DefaultTextBufferPolicy)
	request := Request{Conversation: turn.Conversation, History: turn.History, UserMessage: turn.UserMessage, AssistantMessage: turn.AssistantMessage, RunID: turn.Run.ID, Recorder: recorder}
	go s.execute(runctx, cancel, request, sink, recorder, hubLease, registryLease)
	return result, nil
}

func (s *RunService) execute(ctx context.Context, cancel context.CancelFunc, request Request, sink *bufferedTextSink, recorder *runRecorder, hubLease GenerationHubLease, registryLease GenerationRegistryLease) {
	defer cancel()
	defer registryLease.Release()
	defer hubLease.Release()
	err := s.executor.Stream(ctx, request, sink)
	finalizeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	flushErr := sink.close(finalizeCtx)
	token, final, recordErr := recorder.finishSnapshot()
	status := conversation.MessageStatusCompleted
	code, text := "", ""
	switch {
	case errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled):
		status = conversation.MessageStatusCancelled
	case flushErr != nil || recordErr != nil:
		status = conversation.MessageStatusFailed
		code, text = runFailure("message_persist_failed")
	case err != nil || ctx.Err() != nil:
		status = conversation.MessageStatusFailed
		code, text = runFailure("model_failed")
	case final == nil:
		status = conversation.MessageStatusFailed
		code, text = runFailure("model_failed")
	}
	if status != conversation.MessageStatusCompleted {
		final = nil
	}
	message, finishErr := s.store.FinishAssistant(finalizeCtx, FinishAssistantInput{AssistantMessageID: request.AssistantMessage.ID, Status: status, ErrorCode: code, ErrorMessage: text, ExpectedHead: &token, FinalMessage: final})
	if finishErr != nil {
		slog.Error("Run terminal commit failed", "run_id", request.RunID, "assistant_message_id", request.AssistantMessage.ID, "status", status)
		return
	}
	hubLease.Complete(message)
}

func runFailure(code string) (string, string) {
	switch code {
	case "generation_start_failed":
		return code, "Generation could not start."
	case "message_persist_failed":
		return code, "Generation could not be saved."
	default:
		return "model_failed", "Generation failed."
	}
}

func (s *RunService) Subscribe(ctx context.Context, id string) (GenerationSubscription, error) {
	message, err := s.store.GetMessage(ctx, id)
	if err != nil {
		return GenerationSubscription{}, err
	}
	if message.Role != conversation.MessageRoleAssistant {
		return GenerationSubscription{}, domainerr.ErrInvalidInput
	}
	if message.Status == conversation.MessageStatusStreaming {
		if subscription, ok := s.hub.Subscribe(id); ok {
			return subscription, nil
		}
		message, err = s.store.GetMessage(ctx, id)
		if err != nil {
			return GenerationSubscription{}, err
		}
		if message.Status == conversation.MessageStatusStreaming {
			return GenerationSubscription{}, ErrRunUnavailable
		}
	}
	return GenerationSubscription{Snapshot: GenerationSnapshot{AssistantMessageID: message.ID, Content: message.Content, Status: message.Status}, close: func() {}}, nil
}

func (s *RunService) Cancel(ctx context.Context, id string) (conversation.Message, error) {
	s.startMu.Lock()
	message, err := s.store.GetMessage(ctx, id)
	if err != nil {
		s.startMu.Unlock()
		return conversation.Message{}, err
	}
	if message.Role != conversation.MessageRoleAssistant {
		s.startMu.Unlock()
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	if message.Status != conversation.MessageStatusStreaming {
		s.startMu.Unlock()
		return message, nil
	}
	done, active := s.registry.Cancel(id)
	s.startMu.Unlock()
	if active {
		select {
		case <-done:
		case <-ctx.Done():
			return conversation.Message{}, ctx.Err()
		}
	}
	message, err = s.store.GetMessage(ctx, id)
	if err != nil {
		return conversation.Message{}, err
	}
	if message.Status == conversation.MessageStatusStreaming {
		return conversation.Message{}, ErrRunUnavailable
	}
	return message, nil
}

var _ Service = (*RunService)(nil)
