package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestRunServiceBudgetFailureFeedback(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
		text string
	}{
		{"budget", fmt.Errorf("Graph node: stream reader: %w: private prompt and tool arguments", chat.ErrContextBudgetExceeded), chat.ContextBudgetExceededCode, chat.SafeGenerationErrorText(chat.ContextBudgetExceededCode)},
		{"counting", fmt.Errorf("Graph node: %w: private schema diagnostic", chat.ErrContextCountingFailed), chat.ContextCountingFailedCode, chat.SafeGenerationErrorText(chat.ContextCountingFailedCode)},
		{"ordinary", errors.New("private provider diagnostic"), "model_failed", "Generation failed."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newIntegrationRunFixture(t)
			f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
				if err := sink.WriteChunk(ctx, "partial visible response"); err != nil {
					return err
				}
				if _, err := request.Recorder.RecordAssistant(ctx, integrationFinal("must not be committed")); err != nil {
					return err
				}
				return tc.err
			}
			s := f.service(t)
			started, sub := f.start(t, s)
			f.executor.release()
			updates := integrationDrain(t, f.ctx, sub)
			integrationTerminal(t, updates, conversation.MessageStatusFailed)
			terminal := updates[len(updates)-1]
			if terminal.ErrorCode != tc.code {
				t.Fatalf("live error code = %q, want %q", terminal.ErrorCode, tc.code)
			}
			message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
			if err != nil || message.Content != "partial visible response" || message.ErrorCode != tc.code || message.ErrorMessage != tc.text || strings.Contains(message.ErrorMessage, "private") {
				t.Fatalf("persisted failure = %+v, %v", message, err)
			}
			run := f.run(t)
			if run.Status != agentrun.StatusFailed || run.FinalEntryID != nil || len(f.path(t, nil).Entries) != 1 {
				t.Fatalf("failure committed final: %+v", run)
			}
			reconnected, err := s.Subscribe(f.ctx, started.AssistantMessageID)
			if err != nil || reconnected.Updates != nil || reconnected.Snapshot.Status != terminal.Status || reconnected.Snapshot.ErrorCode != terminal.ErrorCode || reconnected.Snapshot.Content != message.Content {
				t.Fatalf("terminal reconnect = %+v, %v", reconnected, err)
			}
		})
	}
}

func TestRunServiceBudgetFailurePriority(t *testing.T) {
	for _, sentinel := range []error{chat.ErrContextBudgetExceeded, chat.ErrContextCountingFailed} {
		for _, mode := range []string{"cancel", "wrapped cancel", "flush", "recorder", "cancel and persist"} {
			t.Run(sentinel.Error()+"/"+mode, func(t *testing.T) {
				f := newIntegrationRunFixture(t)
				reached := make(chan struct{}, 1)
				if mode == "flush" || mode == "cancel and persist" {
					f.store.appendText = func(context.Context, string, string) error { return errors.New("private checkpoint diagnostic") }
				}
				f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
					if err := sink.WriteChunk(ctx, "partial"); err != nil {
						return err
					}
					if mode == "recorder" {
						// A swallowed recorder error must outrank the executor's budget failure.
						_, _ = request.Recorder.RecordAssistant(ctx, conversation.EntryMessage{Role: conversation.EntryRoleUser})
					} else {
						if _, err := request.Recorder.RecordAssistant(ctx, integrationFinal("discarded final")); err != nil {
							return err
						}
					}
					if mode == "cancel" || mode == "cancel and persist" {
						reached <- struct{}{}
						<-ctx.Done()
					}
					if mode == "wrapped cancel" {
						return errors.Join(sentinel, fmt.Errorf("Graph cancellation: %w", context.Canceled))
					}
					return fmt.Errorf("Graph model: %w", sentinel)
				}
				s := f.service(t)
				started, sub := f.start(t, s)
				f.executor.release()
				wantStatus, wantCode := conversation.MessageStatusFailed, "message_persist_failed"
				if mode == "cancel" || mode == "cancel and persist" {
					integrationReceive(t, f.ctx, reached)
					if _, err := s.Cancel(f.ctx, started.AssistantMessageID); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "cancel" || mode == "wrapped cancel" || mode == "cancel and persist" {
					wantStatus, wantCode = conversation.MessageStatusCancelled, ""
				}
				updates := integrationDrain(t, f.ctx, sub)
				integrationTerminal(t, updates, wantStatus)
				if updates[len(updates)-1].ErrorCode != wantCode {
					t.Fatalf("incorrect terminal: %+v", updates)
				}
				message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
				if err != nil || message.Status != wantStatus || message.ErrorCode != wantCode || f.run(t).FinalEntryID != nil {
					t.Fatalf("priority = %+v, %v", message, err)
				}
				if mode != "flush" && mode != "cancel and persist" && message.Content != "partial" {
					t.Fatalf("lost accepted partial text: %+v", message)
				}
			})
		}
	}
}

func TestRunServiceBudgetTerminalCommitFailureDoesNotBroadcast(t *testing.T) {
	f := newIntegrationRunFixture(t)
	attempts := make(chan chat.FinishAssistantInput, 1)
	f.store.finish = func(_ context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
		attempts <- input.Clone()
		return conversation.Message{}, errors.New("private terminal commit diagnostic")
	}
	f.executor.action = func(ctx context.Context, _ chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "persisted partial"); err != nil {
			return err
		}
		return fmt.Errorf("Graph: %w", chat.ErrContextBudgetExceeded)
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	for _, update := range integrationDrain(t, f.ctx, sub) {
		if update.Kind == chat.GenerationUpdateTerminal {
			t.Fatalf("uncommitted terminal broadcast: %+v", update)
		}
	}
	attempt := integrationReceive(t, f.ctx, attempts)
	if attempt.Status != conversation.MessageStatusFailed || attempt.ErrorCode != chat.ContextBudgetExceededCode || attempt.FinalMessage != nil || attempt.ExpectedHead == nil {
		t.Fatalf("finish attempt = %+v", attempt)
	}
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || message.Status != conversation.MessageStatusStreaming || message.Content != "persisted partial" || message.ErrorCode != "" {
		t.Fatalf("uncommitted failure persisted: %+v, %v", message, err)
	}
	if _, err := s.Subscribe(f.ctx, started.AssistantMessageID); !errors.Is(err, chat.ErrRunUnavailable) {
		t.Fatalf("subscribe after failed commit = %v", err)
	}
}
