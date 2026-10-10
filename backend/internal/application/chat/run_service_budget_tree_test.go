package chat_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func TestRunServiceIntegrationBudgetFailurePreservesToolTree(t *testing.T) {
	for _, failure := range []struct {
		name string
		err  error
		code string
	}{
		{"budget", chat.ErrContextBudgetExceeded, chat.ContextBudgetExceededCode},
		{"counting", chat.ErrContextCountingFailed, chat.ContextCountingFailedCode},
	} {
		for _, stage := range []string{"waiting", "closed", "second waiting"} {
			t.Run(failure.name+"/"+stage, func(t *testing.T) {
				f := newIntegrationRunFixture(t)
				call, results := integrationToolFacts(t, "first-")
				nextCall, _ := integrationToolFacts(t, "second-")
				const partial = "partial visible tool workflow"
				reached := make(chan struct{}, 1)
				finish := make(chan struct{})
				var once sync.Once
				release := func() { once.Do(func() { close(finish) }) }
				t.Cleanup(release)
				f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
					if _, err := request.Recorder.RecordAssistant(ctx, call); err != nil {
						return err
					}
					if stage != "waiting" {
						if err := request.Recorder.RecordToolResults(ctx, results); err != nil {
							return err
						}
					}
					if stage == "second waiting" {
						if _, err := request.Recorder.RecordAssistant(ctx, nextCall); err != nil {
							return err
						}
					}
					if err := sink.WriteChunk(ctx, partial); err != nil {
						return err
					}
					reached <- struct{}{}
					select {
					case <-finish:
						return fmt.Errorf("Graph node: stream reader: %w: private prompt and tool diagnostic", failure.err)
					case <-ctx.Done():
						return ctx.Err()
					}
				}

				// Probe the real transaction with a stale version, then let the
				// service's unchanged token finish through the same SQLite store.
				type finishAttempt struct {
					input        chat.FinishAssistantInput
					staleMessage conversation.Message
					staleErr     error
				}
				attempts := make(chan finishAttempt, 1)
				f.store.finish = func(ctx context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
					attempt := finishAttempt{input: input.Clone()}
					stale := input.Clone()
					if stale.ExpectedHead != nil {
						stale.ExpectedHead.Version--
					}
					attempt.staleMessage, attempt.staleErr = f.repo.FinishAssistant(ctx, stale)
					attempts <- attempt
					return f.repo.FinishAssistant(ctx, input)
				}

				s := f.service(t)
				started, sub := f.start(t, s)
				f.executor.release()
				integrationReceive(t, f.ctx, reached)
				before := f.run(t)
				branch := f.path(t, nil)
				beforeHead, err := f.tree.GetHead(f.ctx, f.input.ConversationID)
				if err != nil || beforeHead.ActiveLeafID == nil || *beforeHead.ActiveLeafID != before.LastEntryID || beforeHead.Version != before.HeadVersion {
					t.Fatalf("fixture head/run mismatch: head=%+v run=%+v err=%v", beforeHead, before, err)
				}
				wantBranchEntries, wantActiveEntries := 2, 1
				wantStatus := agentrun.StatusWaitingTool
				if stage == "closed" {
					wantBranchEntries, wantActiveEntries = 4, 4
					wantStatus = agentrun.StatusRunning
				} else if stage == "second waiting" {
					wantBranchEntries, wantActiveEntries = 5, 4
				}
				if before.Status != wantStatus || len(branch.Entries) != wantBranchEntries || (before.LastEntryID == before.LastClosedEntryID) != (stage == "closed") {
					t.Fatalf("fixture did not reach %s: run=%+v branch=%+v", stage, before, branch)
				}

				release()
				updates := integrationDrain(t, f.ctx, sub)
				integrationTerminal(t, updates, conversation.MessageStatusFailed)
				terminal := updates[len(updates)-1]
				if terminal.ErrorCode != failure.code {
					t.Fatalf("terminal error code = %q, want %q", terminal.ErrorCode, failure.code)
				}
				var streamed strings.Builder
				for _, update := range updates {
					if update.Kind == chat.GenerationUpdateDelta {
						streamed.WriteString(update.Text)
					}
				}
				if streamed.String() != partial {
					t.Fatalf("visible deltas = %q, want %q", streamed.String(), partial)
				}

				attempt := integrationReceive(t, f.ctx, attempts)
				wantText := chat.SafeGenerationErrorText(failure.code)
				if attempt.input.Status != conversation.MessageStatusFailed || attempt.input.ErrorCode != failure.code || attempt.input.ErrorMessage != wantText || attempt.input.FinalMessage != nil || attempt.input.ExpectedHead == nil || attempt.input.ExpectedHead.ActiveLeafID == nil || *attempt.input.ExpectedHead.ActiveLeafID != before.LastEntryID || attempt.input.ExpectedHead.Version != before.HeadVersion {
					t.Fatalf("failure finish lost classification/token: %+v", attempt.input)
				}
				if !errors.Is(attempt.staleErr, domainerr.ErrConflict) || !reflect.DeepEqual(attempt.staleMessage, conversation.Message{}) {
					t.Fatalf("stale failure CAS = %+v, %v", attempt.staleMessage, attempt.staleErr)
				}
				after := f.run(t)
				head, err := f.tree.GetHead(f.ctx, f.input.ConversationID)
				wantVersion := before.HeadVersion
				if stage != "closed" {
					wantVersion++
				}
				if err != nil || head.ActiveLeafID == nil || *head.ActiveLeafID != before.LastClosedEntryID || head.Version != wantVersion || after.HeadVersion != wantVersion || after.LastEntryID != before.LastEntryID || after.LastClosedEntryID != before.LastClosedEntryID || after.Status != agentrun.StatusFailed || after.ErrorCode != failure.code || after.FinalEntryID != nil || after.StartedAt == nil || after.FinishedAt == nil {
					t.Fatalf("incorrect budget failure transaction: before=%+v after=%+v head=%+v err=%v", before, after, head, err)
				}
				if got := f.path(t, &before.LastEntryID); !reflect.DeepEqual(got.Entries, branch.Entries) {
					t.Fatalf("append-only tool facts changed: got=%+v want=%+v", got.Entries, branch.Entries)
				}
				active := f.path(t, nil)
				if !reflect.DeepEqual(active.Entries, branch.Entries[:wantActiveEntries]) {
					t.Fatalf("active path did not retain the closed prefix: %+v", active.Entries)
				}
				message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
				if err != nil || message.Status != conversation.MessageStatusFailed || message.Content != partial || message.ErrorCode != failure.code || message.ErrorMessage != wantText {
					t.Fatalf("persisted budget failure lost partial/classification: %+v, %v", message, err)
				}
				reconnected, err := s.Subscribe(f.ctx, started.AssistantMessageID)
				if err != nil || reconnected.Updates != nil || reconnected.Snapshot.AssistantMessageID != message.ID || reconnected.Snapshot.Status != message.Status || reconnected.Snapshot.ErrorCode != message.ErrorCode || reconnected.Snapshot.Content != message.Content {
					t.Fatalf("terminal reconnect = %+v, %v", reconnected, err)
				}

				next := f.input
				next.ClientMessageID = "next-turn"
				turn, err := f.repo.BeginTurn(f.ctx, chat.BeginTurnInput(next))
				if err != nil || turn.Run == nil || turn.Run.BaseEntryID == nil || *turn.Run.BaseEntryID != before.LastClosedEntryID || turn.Run.BaseHeadVersion != wantVersion || turn.Run.HeadVersion != wantVersion+1 {
					t.Fatalf("next turn did not use the closed head/version: %+v, %v", turn, err)
				}
				if got := f.path(t, &before.LastEntryID); !reflect.DeepEqual(got.Entries, branch.Entries) {
					t.Fatal("next turn changed failed branch facts")
				}
			})
		}
	}
}
