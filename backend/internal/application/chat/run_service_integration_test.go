package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// ignoreEntryID drops the EntryID return value from RecordAssistant for tests
// that don't need to inspect it.
func ignoreEntryID(_ conversation.EntryID, err error) error {
	return err
}

// Faults wrap the real transactional store; successful writes always reach SQLite.
type integrationRunStore struct {
	chat.GenerationStore
	begin      func(context.Context, chat.BeginTurnInput) (chat.BeginTurnResult, error)
	mark       func(context.Context, agentrun.RunID) (agentrun.Run, error)
	commit     func(context.Context, chat.CommitRunMessagesInput) (chat.CommitRunMessagesResult, error)
	finish     func(context.Context, chat.FinishAssistantInput) (conversation.Message, error)
	appendText func(context.Context, string, string) error
}

func (s *integrationRunStore) AppendAssistantText(ctx context.Context, id, text string) error {
	if s.appendText != nil {
		return s.appendText(ctx, id, text)
	}
	return s.GenerationStore.AppendAssistantText(ctx, id, text)
}

func (s *integrationRunStore) BeginTurn(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	if s.begin != nil {
		return s.begin(ctx, input)
	}
	return s.GenerationStore.BeginTurn(ctx, input)
}

func (s *integrationRunStore) MarkRunning(ctx context.Context, id agentrun.RunID) (agentrun.Run, error) {
	if s.mark != nil {
		return s.mark(ctx, id)
	}
	return s.GenerationStore.MarkRunning(ctx, id)
}

func (s *integrationRunStore) CommitRunMessages(ctx context.Context, input chat.CommitRunMessagesInput) (chat.CommitRunMessagesResult, error) {
	if s.commit != nil {
		return s.commit(ctx, input)
	}
	return s.GenerationStore.CommitRunMessages(ctx, input)
}

func (s *integrationRunStore) FinishAssistant(ctx context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
	if s.finish != nil {
		return s.finish(ctx, input)
	}
	return s.GenerationStore.FinishAssistant(ctx, input)
}

type integrationRunContexts func() (context.Context, context.CancelFunc)

func (f integrationRunContexts) New() (context.Context, context.CancelFunc) { return f() }

type integrationRunExecutor struct {
	requests chan chat.Request
	proceed  chan struct{}
	once     sync.Once
	calls    atomic.Int32
	action   func(context.Context, chat.Request, chat.TextSink) error
}

func (e *integrationRunExecutor) release() { e.once.Do(func() { close(e.proceed) }) }

func (e *integrationRunExecutor) Stream(ctx context.Context, request chat.Request, sink chat.TextSink) error {
	e.calls.Add(1)
	e.requests <- request
	select {
	case <-e.proceed:
		return e.action(ctx, request, sink)
	case <-ctx.Done():
		// A cooperative executor is allowed to return nil after cancellation.
		return nil
	}
}

type integrationRunFixture struct {
	ctx      context.Context
	repo     *sqlite.AgentRunRepository
	tree     *sqlite.EntryTreeRepository
	store    *integrationRunStore
	hub      *chat.MemoryGenerationHub
	registry *chat.MemoryGenerationRegistry
	executor *integrationRunExecutor
	contexts chat.RunContextFactory
	input    chat.StartInput
}

func newIntegrationRunFixture(t *testing.T) *integrationRunFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "run-service.db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = db.Close() })
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := sqlite.NewConversationRepository(db).Create(ctx, conversation.Conversation{
		ID: "run-integration", Title: "Run integration", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.NewAgentRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := sqlite.NewEntryTreeRepository(db, sqlite.EntryTreeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	executor := &integrationRunExecutor{requests: make(chan chat.Request, 1), proceed: make(chan struct{})}
	executor.action = func(context.Context, chat.Request, chat.TextSink) error { return nil }
	t.Cleanup(executor.release)
	return &integrationRunFixture{
		ctx: ctx, repo: repo, tree: tree, store: &integrationRunStore{GenerationStore: repo},
		hub: chat.NewMemoryGenerationHub(), registry: chat.NewMemoryGenerationRegistry(), executor: executor,
		contexts: integrationRunContexts(func() (context.Context, context.CancelFunc) { return context.WithCancel(ctx) }),
		input:    chat.StartInput{ConversationID: "run-integration", ClientMessageID: "client-1", Text: "user question"},
	}
}

func (f *integrationRunFixture) service(t *testing.T) *chat.RunService {
	t.Helper()
	s, err := chat.NewRunService(chat.RunDependencies{
		Executor: f.executor, Store: f.store, Hub: f.hub, Registry: f.registry, RunContexts: f.contexts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func integrationReceive[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case value, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before expected value")
		}
		return value
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}

func (f *integrationRunFixture) start(t *testing.T, s *chat.RunService) (chat.StartResult, chat.GenerationSubscription) {
	t.Helper()
	result, err := s.Start(f.ctx, f.input)
	if err != nil {
		t.Fatal(err)
	}
	request := integrationReceive(t, f.ctx, f.executor.requests)
	if request.RunID == "" || request.Recorder == nil || request.AssistantMessage.ID != result.AssistantMessageID {
		t.Fatalf("executor missing explicit Run/Recorder: %+v", request)
	}
	sub, err := s.Subscribe(f.ctx, result.AssistantMessageID)
	if err != nil || sub.Snapshot.Status != conversation.MessageStatusStreaming || sub.Updates == nil {
		t.Fatalf("subscribe = %+v, %v", sub, err)
	}
	t.Cleanup(sub.Close)
	return result, sub
}

func integrationDrain(t *testing.T, ctx context.Context, sub chat.GenerationSubscription) []chat.GenerationUpdate {
	t.Helper()
	var updates []chat.GenerationUpdate
	for {
		select {
		case update, ok := <-sub.Updates:
			if !ok {
				return updates
			}
			updates = append(updates, update)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return nil
		}
	}
}

func integrationTerminal(t *testing.T, updates []chat.GenerationUpdate, want conversation.MessageStatus) {
	t.Helper()
	terminals := 0
	for i, update := range updates {
		if update.Kind == chat.GenerationUpdateTerminal {
			terminals++
			if update.Status != want || i != len(updates)-1 {
				t.Fatalf("terminal out of order or incorrect: %+v", updates)
			}
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal count = %d: %+v", terminals, updates)
	}
}

func (f *integrationRunFixture) run(t *testing.T) agentrun.Run {
	t.Helper()
	turn, err := f.repo.BeginTurn(f.ctx, chat.BeginTurnInput(f.input))
	if err != nil || !turn.Reused || turn.Run == nil {
		t.Fatalf("read Run through idempotency = %+v, %v", turn, err)
	}
	return *turn.Run
}

func (f *integrationRunFixture) path(t *testing.T, leaf *conversation.EntryID) conversation.EntryPath {
	t.Helper()
	path, err := f.tree.ReadPath(f.ctx, conversation.PathQuery{
		ConversationID: f.input.ConversationID, LeafID: leaf, UseActiveLeaf: leaf == nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func integrationFinal(text string) conversation.EntryMessage {
	return conversation.EntryMessage{
		Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopNormal,
		Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}},
	}
}

func integrationToolFacts(t *testing.T, prefix string) (conversation.EntryMessage, []conversation.EntryMessage) {
	t.Helper()
	policy := conversation.ToolPersistencePolicy{
		ToolName: "run-service-integration-search", Version: "v1", MaxPreviewBytes: 32,
		ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 32}},
		ResultFields:   []conversation.ProjectionFieldRule{{SourceKey: "answer", StoredKey: "answer", MaxBytes: 32}},
	}
	if _, exists := conversation.GetToolPolicy(policy.ToolName, policy.Version); !exists {
		if err := conversation.RegisterToolPolicy(policy); err != nil {
			t.Fatal(err)
		}
	}
	message := conversation.EntryMessage{Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopTools}
	var results []conversation.EntryMessage
	for _, suffix := range []string{"1", "2"} {
		call, err := conversation.ProjectSafeToolCall(policy, prefix+suffix, json.RawMessage(`{"q":"approved","private":"must not persist"}`))
		if err != nil {
			t.Fatal(err)
		}
		message.Content = append(message.Content, conversation.EntryPart{Type: conversation.EntryPartToolCall, ToolCall: &call})
		result, err := conversation.ProjectSafeToolResult(policy, json.RawMessage(`{"answer":"approved","private":"must not persist"}`))
		if err != nil {
			t.Fatal(err)
		}
		// The Recorder binds the actual committed call Entry ID, not the executor.
		results = append([]conversation.EntryMessage{{Role: conversation.EntryRoleToolResult,
			ToolResult: &conversation.EntryToolResult{CallID: prefix + suffix, ToolName: policy.ToolName, Result: result, IsError: suffix == "2"},
		}}, results...)
	}
	return message, results
}

func TestRunServiceIntegrationSuccessfulFinalIsNotVisibleTranscript(t *testing.T) {
	f := newIntegrationRunFixture(t)
	call, results := integrationToolFacts(t, "call-")
	final := integrationFinal("complete final response, not the visible transcript")
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "visible prelude; "); err != nil {
			return err
		}
		if _, err := request.Recorder.RecordAssistant(ctx, call); err != nil {
			return err
		}
		if err := request.Recorder.RecordToolResults(ctx, results); err != nil {
			return err
		}
		if err := sink.WriteChunk(ctx, "visible streamed answer"); err != nil {
			return err
		}
		_, err := request.Recorder.RecordAssistant(ctx, final); return err
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	updates := integrationDrain(t, f.ctx, sub)
	integrationTerminal(t, updates, conversation.MessageStatusCompleted)
	var streamed strings.Builder
	for _, update := range updates {
		if update.Kind == chat.GenerationUpdateDelta {
			streamed.WriteString(update.Text)
		}
	}
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || message.Content != "visible prelude; visible streamed answer" || streamed.String() != message.Content {
		t.Fatalf("legacy transcript/deltas = %+v, %q, %v", message, streamed.String(), err)
	}
	run := f.run(t)
	path := f.path(t, nil)
	if run.Status != agentrun.StatusCompleted || run.FinalEntryID == nil || run.FinishedAt == nil || run.StartedAt == nil || len(path.Entries) != 5 {
		t.Fatalf("completed Run/path = %+v, %+v", run, path)
	}
	if path.Entries[4].ID != *run.FinalEntryID || !reflect.DeepEqual(path.Entries[4].Message, final) || run.LastClosedEntryID != *run.FinalEntryID {
		t.Fatalf("full final was invented or replaced by transcript: %+v", path)
	}
	for i, id := range []string{"call-1", "call-2"} {
		result := path.Entries[i+2].Message.ToolResult
		if result == nil || result.CallID != id || result.CallEntryID != path.Entries[1].ID || result.IsError != (i == 1) {
			t.Fatalf("tool group not bound/ordered: %+v", path)
		}
	}
	encoded, err := json.Marshal(path.Entries)
	if err != nil || strings.Contains(string(encoded), "must not persist") || strings.Contains(string(encoded), `"private"`) {
		t.Fatalf("unprojected tool payload persisted: %s, %v", encoded, err)
	}
	retry, err := s.Start(f.ctx, f.input)
	if err != nil || retry != started || f.executor.calls.Load() != 1 || !reflect.DeepEqual(f.path(t, nil), path) {
		t.Fatalf("terminal retry executed or changed facts: %+v, %v", retry, err)
	}
	reconnected, err := s.Subscribe(f.ctx, started.AssistantMessageID)
	if err != nil || reconnected.Snapshot.Status != conversation.MessageStatusCompleted || reconnected.Snapshot.Content != message.Content || reconnected.Updates != nil {
		t.Fatalf("terminal reconnect = %+v, %v", reconnected, err)
	}
	if _, active := f.registry.Cancel(started.AssistantMessageID); active {
		t.Fatal("completed execution retained registry ownership")
	}
}

func TestRunServiceIntegrationMissingFinalOrIgnoredRecordErrorFails(t *testing.T) {
	for _, mode := range []string{"missing final", "ignored validation error", "ignored store error"} {
		t.Run(mode, func(t *testing.T) {
			f := newIntegrationRunFixture(t)
			call, _ := integrationToolFacts(t, "ignored-")
			var commits atomic.Int32
			if mode == "ignored store error" {
				f.store.commit = func(context.Context, chat.CommitRunMessagesInput) (chat.CommitRunMessagesResult, error) {
					commits.Add(1)
					return chat.CommitRunMessagesResult{}, errors.New("private database diagnostic")
				}
			}
			recordErrors := make(chan error, 1)
			f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
				if err := sink.WriteChunk(ctx, "partial visible text is not a final"); err != nil {
					return err
				}
				switch mode {
				case "ignored validation error":
					recordErrors <- ignoreEntryID(request.Recorder.RecordAssistant(ctx, conversation.EntryMessage{Role: conversation.EntryRoleUser}))
				case "ignored store error":
					recordErrors <- ignoreEntryID(request.Recorder.RecordAssistant(ctx, call))
				}
				if mode != "missing final" {
					// Ignoring the first error and supplying a valid final cannot heal it.
					_ = ignoreEntryID(request.Recorder.RecordAssistant(ctx, integrationFinal("must not be committed")))
				}
				return nil
			}
			s := f.service(t)
			started, sub := f.start(t, s)
			f.executor.release()
			integrationTerminal(t, integrationDrain(t, f.ctx, sub), conversation.MessageStatusFailed)
			if mode != "missing final" && integrationReceive(t, f.ctx, recordErrors) == nil {
				t.Fatal("fixture did not produce a Record error")
			}
			message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
			wantCode := "message_persist_failed"
			if mode == "missing final" {
				wantCode = "model_failed"
			}
			if err != nil || message.Status != conversation.MessageStatusFailed || message.ErrorCode != wantCode || message.Content != "partial visible text is not a final" || strings.Contains(message.ErrorMessage, "private") {
				t.Fatalf("failed message = %+v, %v", message, err)
			}
			run := f.run(t)
			if run.Status != agentrun.StatusFailed || run.FinalEntryID != nil || len(f.path(t, nil).Entries) != 1 || (mode == "ignored store error" && commits.Load() != 1) {
				t.Fatalf("failure fabricated final or retried write: %+v, commits=%d", run, commits.Load())
			}
		})
	}
}

func TestRunServiceIntegrationCancelWinsWhenExecutorReturnsNil(t *testing.T) {
	f := newIntegrationRunFixture(t)
	recorded := make(chan struct{}, 1)
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "partial before cancellation"); err != nil {
			return err
		}
		if _, err := request.Recorder.RecordAssistant(ctx, integrationFinal("uncommitted complete response")); err != nil {
			return err
		}
		recorded <- struct{}{}
		<-ctx.Done()
		return nil
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	integrationReceive(t, f.ctx, recorded)
	message, err := s.Cancel(f.ctx, started.AssistantMessageID)
	if err != nil || message.Status != conversation.MessageStatusCancelled || message.Content != "partial before cancellation" {
		t.Fatalf("Cancel = %+v, %v", message, err)
	}
	integrationTerminal(t, integrationDrain(t, f.ctx, sub), conversation.MessageStatusCancelled)
	run := f.run(t)
	if run.Status != agentrun.StatusCancelled || run.FinalEntryID != nil || len(f.path(t, nil).Entries) != 1 {
		t.Fatalf("cancelled final was committed: %+v", run)
	}
}

func TestRunServiceIntegrationRootCancellationFinalizesWithoutCommittingFinal(t *testing.T) {
	f := newIntegrationRunFixture(t)
	root, stopRoot := context.WithCancel(f.ctx)
	t.Cleanup(stopRoot)
	f.contexts = chat.NewDetachedRunContextFactory(root, time.Minute)
	recorded := make(chan struct{}, 1)
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "partial before root cancellation"); err != nil {
			return err
		}
		if _, err := request.Recorder.RecordAssistant(ctx, integrationFinal("final discarded on shutdown")); err != nil {
			return err
		}
		recorded <- struct{}{}
		<-ctx.Done()
		return nil
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	integrationReceive(t, f.ctx, recorded)
	stopRoot()
	integrationTerminal(t, integrationDrain(t, f.ctx, sub), conversation.MessageStatusCancelled)
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || message.Status != conversation.MessageStatusCancelled || message.Content != "partial before root cancellation" || message.ErrorCode != "" || message.ErrorMessage != "" {
		t.Fatalf("root cancellation did not persist partial/terminal: %+v, %v", message, err)
	}
	run := f.run(t)
	path := f.path(t, nil)
	if run.Status != agentrun.StatusCancelled || run.StartedAt == nil || run.FinishedAt == nil || run.FinalEntryID != nil || len(path.Entries) != 1 || path.Entries[0].ID != run.LastClosedEntryID {
		t.Fatalf("root cancellation committed final or failed to finalize: %+v, %+v", run, path)
	}
	reconnected, err := s.Subscribe(f.ctx, started.AssistantMessageID)
	if err != nil || reconnected.Snapshot.Status != conversation.MessageStatusCancelled || reconnected.Snapshot.Content != message.Content || reconnected.Updates != nil {
		t.Fatalf("root cancellation reconnect = %+v, %v", reconnected, err)
	}
	next := f.input
	next.ClientMessageID = "after-root-cancellation"
	if _, err := f.repo.BeginTurn(f.ctx, chat.BeginTurnInput(next)); err != nil {
		t.Fatalf("root cancellation left conversation busy: %v", err)
	}
}

func TestRunServiceIntegrationBrowserCancellationDoesNotCancelRun(t *testing.T) {
	f := newIntegrationRunFixture(t)
	f.contexts = chat.NewDetachedRunContextFactory(f.ctx, time.Minute)
	browser, stopBrowser := context.WithCancel(f.ctx)
	t.Cleanup(stopBrowser)
	executorContext := make(chan error, 1)
	final := integrationFinal("complete response after browser disconnect")
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		executorContext <- ctx.Err()
		if err := sink.WriteChunk(ctx, "visible response after browser disconnect"); err != nil {
			return err
		}
		_, err := request.Recorder.RecordAssistant(ctx, final); return err
	}
	s := f.service(t)
	started, err := s.Start(browser, f.input)
	if err != nil {
		t.Fatal(err)
	}
	request := integrationReceive(t, f.ctx, f.executor.requests)
	if request.RunID == "" || request.Recorder == nil || request.AssistantMessage.ID != started.AssistantMessageID {
		t.Fatalf("executor missing explicit Run/Recorder: %+v", request)
	}
	disconnected, err := s.Subscribe(browser, started.AssistantMessageID)
	if err != nil || disconnected.Snapshot.Status != conversation.MessageStatusStreaming || disconnected.Updates == nil {
		t.Fatalf("browser subscribe = %+v, %v", disconnected, err)
	}
	t.Cleanup(disconnected.Close)
	stopBrowser()
	disconnected.Close()
	if browser.Err() != context.Canceled {
		t.Fatalf("fixture did not cancel browser: %v", browser.Err())
	}
	retry, err := s.Start(f.ctx, f.input)
	if err != nil || retry != started || f.executor.calls.Load() != 1 {
		t.Fatalf("browser reconnect duplicated execution: %+v, %v, calls=%d", retry, err, f.executor.calls.Load())
	}
	sub, err := s.Subscribe(f.ctx, started.AssistantMessageID)
	if err != nil || sub.Snapshot.Status != conversation.MessageStatusStreaming || sub.Updates == nil {
		t.Fatalf("browser disconnect removed active generation: %+v, %v", sub, err)
	}
	t.Cleanup(sub.Close)
	f.executor.release()
	integrationTerminal(t, integrationDrain(t, f.ctx, sub), conversation.MessageStatusCompleted)
	if err := integrationReceive(t, f.ctx, executorContext); err != nil {
		t.Fatalf("browser cancellation propagated to executor: %v", err)
	}
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || message.Status != conversation.MessageStatusCompleted || message.Content != "visible response after browser disconnect" {
		t.Fatalf("browser cancellation interrupted persisted response: %+v, %v", message, err)
	}
	run := f.run(t)
	path := f.path(t, nil)
	if run.Status != agentrun.StatusCompleted || run.FinishedAt == nil || run.FinalEntryID == nil || len(path.Entries) != 2 || path.Entries[1].ID != *run.FinalEntryID || !reflect.DeepEqual(path.Entries[1].Message, final) {
		t.Fatalf("browser cancellation lost authoritative final: %+v, %+v", run, path)
	}
}

func TestRunServiceIntegrationStartupFailureCompensatesWithoutExecutor(t *testing.T) {
	for _, mode := range []string{"MarkRunning", "invalid context", "invalid recorder preparation"} {
		t.Run(mode, func(t *testing.T) {
			f := newIntegrationRunFixture(t)
			fault := errors.New("private startup diagnostic")
			var cancelled atomic.Int32
			f.contexts = integrationRunContexts(func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(f.ctx)
				if mode == "invalid context" {
					ctx = nil
				}
				return ctx, func() { cancelled.Add(1); cancel() }
			})
			switch mode {
			case "MarkRunning":
				f.store.mark = func(context.Context, agentrun.RunID) (agentrun.Run, error) { return agentrun.Run{}, fault }
			case "invalid recorder preparation":
				f.store.begin = func(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
					turn, err := f.repo.BeginTurn(ctx, input)
					if err == nil {
						// Corrupt only the returned preparation state, never persistent facts.
						turn.Run.Status = agentrun.StatusWaitingTool
					}
					return turn, err
				}
			}
			s := f.service(t)
			_, err := s.Start(f.ctx, f.input)
			if err == nil || (mode == "MarkRunning" && !errors.Is(err, fault)) || f.executor.calls.Load() != 0 || cancelled.Load() != 1 {
				t.Fatalf("startup = %v, executor=%d cancel=%d", err, f.executor.calls.Load(), cancelled.Load())
			}
			run := f.run(t)
			message, err := f.repo.GetMessage(f.ctx, run.AssistantMessageID)
			if err != nil || run.Status != agentrun.StatusFailed || run.StartedAt != nil || run.FinishedAt == nil || run.FinalEntryID != nil || message.Status != conversation.MessageStatusFailed || message.ErrorCode != "generation_start_failed" || strings.Contains(message.ErrorMessage, "private") {
				t.Fatalf("startup did not compensate atomically: %+v, %+v, %v", run, message, err)
			}
			if len(f.path(t, nil).Entries) != 1 {
				t.Fatal("startup failure invented assistant Entry")
			}
			if _, active := f.registry.Cancel(run.AssistantMessageID); active {
				t.Fatal("startup failure leaked registry lease")
			}
			if sub, active := f.hub.Subscribe(run.AssistantMessageID); active {
				sub.Close()
				t.Fatal("startup failure leaked hub lease")
			}
			next := f.input
			next.ClientMessageID = "after-startup-failure"
			if _, err := f.repo.BeginTurn(f.ctx, chat.BeginTurnInput(next)); err != nil {
				t.Fatalf("compensation left conversation busy: %v", err)
			}
		})
	}
}

func TestRunServiceIntegrationClaimConflictPreservesExistingOwner(t *testing.T) {
	for _, resource := range []string{"registry", "hub"} {
		t.Run(resource, func(t *testing.T) {
			f := newIntegrationRunFixture(t)
			var ownerCancelled atomic.Int32
			var ownerRegistry chat.GenerationRegistryLease
			var ownerHub chat.GenerationHubLease
			var original chat.GenerationSubscription
			f.store.begin = func(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
				turn, err := f.repo.BeginTurn(ctx, input)
				if err != nil {
					return turn, err
				}
				var ok bool
				message := turn.AssistantMessage
				message.Content = "existing owner's snapshot"
				ownerHub, ok = f.hub.Claim(message)
				if !ok {
					return turn, fmt.Errorf("fixture could not claim hub")
				}
				original, ok = f.hub.Subscribe(message.ID)
				if !ok {
					return turn, fmt.Errorf("fixture could not subscribe")
				}
				if resource == "registry" {
					ownerRegistry, ok = f.registry.Claim(message.ID, func() { ownerCancelled.Add(1) })
					if !ok {
						return turn, fmt.Errorf("fixture could not claim registry")
					}
				}
				return turn, nil
			}
			t.Cleanup(func() {
				original.Close()
				if ownerHub != nil {
					ownerHub.Release()
				}
				if ownerRegistry != nil {
					ownerRegistry.Release()
				}
			})
			s := f.service(t)
			_, err := s.Start(f.ctx, f.input)
			if !errors.Is(err, domainerr.ErrConflict) || f.executor.calls.Load() != 0 || ownerCancelled.Load() != 0 {
				t.Fatalf("conflict = %v, executor=%d owner cancelled=%d", err, f.executor.calls.Load(), ownerCancelled.Load())
			}
			run := f.run(t)
			if run.Status != agentrun.StatusPending || run.FinishedAt != nil {
				t.Fatalf("claim loser compensated another owner's Run: %+v", run)
			}
			current, ok := f.hub.Subscribe(run.AssistantMessageID)
			if !ok || current.Snapshot.Content != original.Snapshot.Content {
				t.Fatalf("existing hub snapshot replaced: %+v", current)
			}
			current.Close()
			f.hub.PublishText(run.AssistantMessageID, "; owner continues")
			if update := integrationReceive(t, f.ctx, original.Updates); update.Kind != chat.GenerationUpdateDelta || update.Text != "; owner continues" {
				t.Fatalf("existing subscriber disturbed: %+v", update)
			}
			done, active := f.registry.Cancel(run.AssistantMessageID)
			if resource == "registry" {
				if !active || ownerCancelled.Load() != 1 {
					t.Fatal("existing registry owner removed or replaced")
				}
				select {
				case <-done:
					t.Fatal("existing owner prematurely released")
				default:
				}
			} else if active {
				t.Fatal("failed hub claim leaked newly acquired registry lease")
			}
		})
	}
}

func TestRunServiceIntegrationCancelledTimerCheckpointPreservesAcceptedTail(t *testing.T) {
	f := newIntegrationRunFixture(t)
	accepted := make(chan struct{}, 1)
	checkpoint := make(chan error, 1)
	f.store.appendText = func(ctx context.Context, id, text string) error {
		err := f.repo.AppendAssistantText(ctx, id, text)
		checkpoint <- err
		return err
	}
	f.executor.action = func(ctx context.Context, _ chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "tail"); err != nil {
			return err
		}
		accepted <- struct{}{}
		<-ctx.Done()
		// Hold the executor until the timer attempts its checkpoint. The
		// independent finalizer cannot make this write succeed for it.
		select {
		case err := <-checkpoint:
			return err
		case <-f.ctx.Done():
			return f.ctx.Err()
		}
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	integrationReceive(t, f.ctx, accepted)
	message, err := s.Cancel(f.ctx, started.AssistantMessageID)
	if err != nil || message.Status != conversation.MessageStatusCancelled || message.Content != "tail" {
		t.Fatalf("cancel lost checkpoint: %+v %v", message, err)
	}
	updates := integrationDrain(t, f.ctx, sub)
	integrationTerminal(t, updates, conversation.MessageStatusCancelled)
	if len(updates) < 2 || updates[0].Kind != chat.GenerationUpdateDelta || updates[0].Text != "tail" {
		t.Fatalf("accepted tail not published: %+v", updates)
	}
}

func TestRunServiceIntegrationFlushFailureRejectsCompletedFinal(t *testing.T) {
	f := newIntegrationRunFixture(t)
	f.store.appendText = func(context.Context, string, string) error { return errors.New("checkpoint commit rejected") }
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "buffered text"); err != nil {
			return err
		}
		_, err := request.Recorder.RecordAssistant(ctx, integrationFinal("uncommitted final")); return err
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	updates := integrationDrain(t, f.ctx, sub)
	integrationTerminal(t, updates, conversation.MessageStatusFailed)
	for _, update := range updates {
		if update.Kind == chat.GenerationUpdateDelta {
			t.Fatal("uncommitted checkpoint published")
		}
	}
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || message.Content != "" || message.ErrorCode != "message_persist_failed" || f.run(t).FinalEntryID != nil || len(f.path(t, nil).Entries) != 1 {
		t.Fatalf("flush failure committed a final: %+v, %v", message, err)
	}
}

func TestRunServiceIntegrationCompensationFailureKeepsDiagnosablePendingRun(t *testing.T) {
	f := newIntegrationRunFixture(t)
	f.store.mark = func(context.Context, agentrun.RunID) (agentrun.Run, error) {
		return agentrun.Run{}, errors.New("startup rejected")
	}
	var calls atomic.Int32
	f.store.finish = func(context.Context, chat.FinishAssistantInput) (conversation.Message, error) {
		calls.Add(1)
		return conversation.Message{}, errors.New("compensation rejected")
	}
	s := f.service(t)
	if _, err := s.Start(f.ctx, f.input); !errors.Is(err, chat.ErrRunUnavailable) {
		t.Fatalf("start: %v", err)
	}
	run := f.run(t)
	message, err := f.repo.GetMessage(f.ctx, run.AssistantMessageID)
	if err != nil || calls.Load() != 1 || f.executor.calls.Load() != 0 || run.Status != agentrun.StatusPending || message.Status != conversation.MessageStatusStreaming {
		t.Fatalf("failed compensation hid durable pending run: %+v %+v %v", run, message, err)
	}
	if _, active := f.registry.Cancel(message.ID); active {
		t.Fatal("compensation failure leaked execution")
	}
	if sub, active := f.hub.Subscribe(message.ID); active {
		sub.Close()
		t.Fatal("compensation failure leaked hub")
	}
	if _, err := s.Subscribe(f.ctx, message.ID); !errors.Is(err, chat.ErrRunUnavailable) {
		t.Fatalf("subscribe: %v", err)
	}
}

func TestRunServiceIntegrationFinishFailureDoesNotBroadcastTerminal(t *testing.T) {
	f := newIntegrationRunFixture(t)
	attempts := make(chan chat.FinishAssistantInput, 1)
	f.store.finish = func(_ context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
		attempts <- input.Clone()
		return conversation.Message{}, errors.New("terminal transaction rejected")
	}
	f.executor.action = func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
		if err := sink.WriteChunk(ctx, "persisted visible text"); err != nil {
			return err
		}
		_, err := request.Recorder.RecordAssistant(ctx, integrationFinal("final must remain uncommitted")); return err
	}
	s := f.service(t)
	started, sub := f.start(t, s)
	f.executor.release()
	updates := integrationDrain(t, f.ctx, sub)
	for _, update := range updates {
		if update.Kind == chat.GenerationUpdateTerminal {
			t.Fatalf("uncommitted terminal broadcast: %+v", updates)
		}
	}
	attempt := integrationReceive(t, f.ctx, attempts)
	if attempt.Status != conversation.MessageStatusCompleted || attempt.FinalMessage == nil || attempt.ExpectedHead == nil {
		t.Fatalf("finish did not receive explicit final/token: %+v", attempt)
	}
	if _, err := s.Cancel(f.ctx, started.AssistantMessageID); !errors.Is(err, chat.ErrRunUnavailable) {
		t.Fatalf("Cancel after failed commit = %v", err)
	}
	if _, err := s.Subscribe(f.ctx, started.AssistantMessageID); !errors.Is(err, chat.ErrRunUnavailable) {
		t.Fatalf("Subscribe after failed commit = %v", err)
	}
	run := f.run(t)
	message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
	if err != nil || run.Status != agentrun.StatusRunning || run.FinalEntryID != nil || run.FinishedAt != nil || message.Status != conversation.MessageStatusStreaming || message.Content != "persisted visible text" || len(f.path(t, nil).Entries) != 1 {
		t.Fatalf("failed Finish changed durable terminal facts: %+v, %+v, %v", run, message, err)
	}
	if _, active := f.registry.Cancel(started.AssistantMessageID); active {
		t.Fatal("failed Finish leaked registry ownership")
	}
}

func TestRunServiceIntegrationToolFailureOrCancelRewindsAndPreservesBranch(t *testing.T) {
	for _, stage := range []string{"waiting", "closed", "second waiting"} {
		for _, status := range []conversation.MessageStatus{conversation.MessageStatusFailed, conversation.MessageStatusCancelled} {
			t.Run(stage+"/"+string(status), func(t *testing.T) {
				f := newIntegrationRunFixture(t)
				call, results := integrationToolFacts(t, "first-")
				nextCall, _ := integrationToolFacts(t, "second-")
				reached := make(chan struct{}, 1)
				finish := make(chan struct{})
				var finishOnce sync.Once
				release := func() { finishOnce.Do(func() { close(finish) }) }
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
					if err := sink.WriteChunk(ctx, "partial tool workflow"); err != nil {
						return err
					}
					reached <- struct{}{}
					select {
					case <-ctx.Done():
						return nil
					case <-finish:
						return errors.New("private upstream model diagnostic")
					}
				}
				s := f.service(t)
				started, sub := f.start(t, s)
				f.executor.release()
				integrationReceive(t, f.ctx, reached)
				before := f.run(t)
				branch := f.path(t, nil)
				if stage == "closed" && before.LastEntryID != before.LastClosedEntryID {
					t.Fatal("fixture did not close tool group")
				}
				if stage != "closed" && before.Status != agentrun.StatusWaitingTool {
					t.Fatal("fixture did not reach pending tool group")
				}
				if status == conversation.MessageStatusCancelled {
					message, err := s.Cancel(f.ctx, started.AssistantMessageID)
					if err != nil || message.Status != status {
						t.Fatalf("Cancel = %+v, %v", message, err)
					}
				} else {
					release()
				}
				integrationTerminal(t, integrationDrain(t, f.ctx, sub), status)
				after := f.run(t)
				head, err := f.tree.GetHead(f.ctx, f.input.ConversationID)
				wantVersion := before.HeadVersion
				if before.LastEntryID != before.LastClosedEntryID {
					wantVersion++
				}
				if err != nil || head.ActiveLeafID == nil || *head.ActiveLeafID != before.LastClosedEntryID || head.Version != wantVersion || after.HeadVersion != wantVersion || after.LastEntryID != before.LastEntryID || after.LastClosedEntryID != before.LastClosedEntryID || after.FinalEntryID != nil || after.FinishedAt == nil || string(after.Status) != string(status) {
					t.Fatalf("incorrect rollback: before=%+v after=%+v head=%+v err=%v", before, after, head, err)
				}
				if got := f.path(t, &before.LastEntryID); !reflect.DeepEqual(got.Entries, branch.Entries) {
					t.Fatalf("immutable abandoned branch changed: %+v", got)
				}
				active := f.path(t, nil)
				wantEntries := 4
				if stage == "waiting" {
					wantEntries = 1
				}
				if len(active.Entries) != wantEntries {
					t.Fatalf("active path retained incomplete calls: %+v", active)
				}
				message, err := f.repo.GetMessage(f.ctx, started.AssistantMessageID)
				if err != nil || message.Content != "partial tool workflow" || strings.Contains(message.ErrorMessage, "private") {
					t.Fatalf("legacy partial/error = %+v, %v", message, err)
				}
				next := f.input
				next.ClientMessageID = "next-turn"
				turn, err := f.repo.BeginTurn(f.ctx, chat.BeginTurnInput(next))
				if err != nil || turn.Run.BaseEntryID == nil || *turn.Run.BaseEntryID != before.LastClosedEntryID || turn.Run.BaseHeadVersion != wantVersion {
					t.Fatalf("next turn did not use real rollback head: %+v, %v", turn, err)
				}
				if got := f.path(t, &before.LastEntryID); !reflect.DeepEqual(got.Entries, branch.Entries) {
					t.Fatal("new turn removed failed branch")
				}
			})
		}
	}
}

func TestRunServiceIntegrationCancelDuringStartRegistrationWindow(t *testing.T) {
	f := newIntegrationRunFixture(t)
	prepared := make(chan chat.BeginTurnResult, 1)
	register := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(register) }) }
	t.Cleanup(release)
	f.store.begin = func(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
		turn, err := f.repo.BeginTurn(ctx, input)
		if err != nil {
			return turn, err
		}
		prepared <- turn.Clone()
		select {
		case <-register:
			return turn, nil
		case <-ctx.Done():
			return chat.BeginTurnResult{}, ctx.Err()
		}
	}
	s := f.service(t)
	type startOutcome struct {
		result chat.StartResult
		err    error
	}
	started := make(chan startOutcome, 1)
	go func() { result, err := s.Start(f.ctx, f.input); started <- startOutcome{result, err} }()
	turn := integrationReceive(t, f.ctx, prepared)
	if turn.Run.Status != agentrun.StatusPending {
		t.Fatal("gate is not before registration")
	}
	if _, active := f.registry.Cancel(turn.AssistantMessage.ID); active {
		t.Fatal("registry was claimed before gate")
	}
	type cancelOutcome struct {
		message conversation.Message
		err     error
	}
	cancelEntered := make(chan struct{})
	cancelled := make(chan cancelOutcome, 1)
	go func() {
		close(cancelEntered)
		message, err := s.Cancel(f.ctx, turn.AssistantMessage.ID)
		cancelled <- cancelOutcome{message, err}
	}()
	integrationReceive(t, f.ctx, func() <-chan struct{} {
		// Convert the entry signal to a value channel for the bounded receive helper.
		ch := make(chan struct{}, 1)
		go func() { <-cancelEntered; ch <- struct{}{} }()
		return ch
	}())
	release()
	start := integrationReceive(t, f.ctx, started)
	cancel := integrationReceive(t, f.ctx, cancelled)
	if start.err != nil || start.result.AssistantMessageID != turn.AssistantMessage.ID || cancel.err != nil || cancel.message.Status != conversation.MessageStatusCancelled || f.executor.calls.Load() != 1 {
		t.Fatalf("registration race lost cancellation: Start=%+v Cancel=%+v calls=%d", start, cancel, f.executor.calls.Load())
	}
	run := f.run(t)
	if run.Status != agentrun.StatusCancelled || run.FinalEntryID != nil || len(f.path(t, nil).Entries) != 1 {
		t.Fatalf("registration race durable result = %+v", run)
	}
	if _, active := f.registry.Cancel(turn.AssistantMessage.ID); active {
		t.Fatal("registration race leaked registry")
	}
	if sub, active := f.hub.Subscribe(turn.AssistantMessage.ID); active {
		sub.Close()
		t.Fatal("registration race leaked hub")
	}
}

var _ chat.GenerationStore = (*integrationRunStore)(nil)
var _ chat.Executor = (*integrationRunExecutor)(nil)

// TestRunServiceIntegrationToolCallsAndResultsWriteToEntryTree verifies that
// tool calls and results are correctly recorded in the Entry Tree through the
// Graph → RunRecorder → Entry Tree pipeline.
