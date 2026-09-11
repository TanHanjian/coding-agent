package httpchat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// controlledExecutor 模拟一个可由测试精确推进的模型流：首次调用会等待 release，
// 因而测试可以在生成仍处于 streaming 时发起重试、取消或重新订阅。
type controlledExecutor struct {
	initial string
	final   string

	started          chan struct{}
	initialCommitted chan struct{}
	release          chan struct{}

	mu    sync.Mutex
	calls int
	once  sync.Once
}

func newControlledExecutor(initial, final string) *controlledExecutor {
	return &controlledExecutor{
		initial:          initial,
		final:            final,
		started:          make(chan struct{}),
		initialCommitted: make(chan struct{}),
		release:          make(chan struct{}),
	}
}

func (e *controlledExecutor) Stream(ctx context.Context, _ chat.Request, sink chat.TextSink) error {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
	e.once.Do(func() { close(e.started) })

	if e.initial != "" {
		if err := sink.WriteChunk(ctx, e.initial); err != nil {
			return err
		}
		close(e.initialCommitted)
	}

	select {
	case <-e.release:
		if e.final == "" {
			return nil
		}
		return sink.WriteChunk(ctx, e.final)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *controlledExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type observingHub struct {
	*chat.MemoryGenerationHub
	subscribed chan string
}

func newObservingHub() *observingHub {
	return &observingHub{
		MemoryGenerationHub: chat.NewMemoryGenerationHub(),
		subscribed:          make(chan string, 4),
	}
}

func (h *observingHub) Subscribe(assistantMessageID string) (chat.GenerationSubscription, bool) {
	subscription, ok := h.MemoryGenerationHub.Subscribe(assistantMessageID)
	if ok {
		h.subscribed <- assistantMessageID
	}
	return subscription, ok
}

type chatHTTPIntegration struct {
	handler        http.Handler
	conversationID string
	messages       *sqlite.MessageRepository
	executor       *controlledExecutor
	hub            *observingHub
}

func newChatHTTPIntegration(t *testing.T, executor *controlledExecutor) *chatHTTPIntegration {
	t.Helper()
	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(t.Context(), db.DB); err != nil {
		t.Fatal(err)
	}

	conversations := sqlite.NewConversationRepository(db)
	messages := sqlite.NewMessageRepository(db)
	conversationService := conversation.NewService(conversation.Dependencies{
		Conversations: conversations,
		Messages:      messages,
	})
	conversationRecord, err := conversationService.CreateConversation(t.Context(), conversation.CreateConversationInput{Title: "集成测试"})
	if err != nil {
		t.Fatal(err)
	}
	hub := newObservingHub()
	service, err := chat.NewSkeletonService(chat.Dependencies{
		Executor:    executor,
		Registry:    chat.NewMemoryGenerationRegistry(),
		Hub:         hub,
		RunContexts: chat.NewDetachedRunContextFactory(context.Background(), time.Minute),
		Store:       sqlite.NewChatTurnRepository(db),
	})
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	RegisterRoutes(router, service)

	return &chatHTTPIntegration{
		handler:        router,
		conversationID: conversationRecord.ID,
		messages:       messages,
		executor:       executor,
		hub:            hub,
	}
}

func (a *chatHTTPIntegration) post(conversationID, clientMessageID, text string) *httptest.ResponseRecorder {
	payload, err := json.Marshal(startRequest{
		ID: conversationID,
		Message: uiMessage{
			ID:   clientMessageID,
			Role: "user",
			Parts: []messagePart{{
				Type: "text",
				Text: text,
			}},
		},
	})
	if err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/chat", bytes.NewReader(payload))
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, request)
	return response
}

func (a *chatHTTPIntegration) postAsync(conversationID, clientMessageID, text string) <-chan *httptest.ResponseRecorder {
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- a.post(conversationID, clientMessageID, text) }()
	return result
}

func TestChatHTTPIntegrationStreamsOnePersistedTurnForIdempotentRetry(t *testing.T) {
	executor := newControlledExecutor("", "回答")
	app := newChatHTTPIntegration(t, executor)
	first := app.postAsync(app.conversationID, "client-1", "解释边界")
	waitForExecutorStart(t, executor, first)
	waitForSubscription(t, app.hub)

	// 同一幂等键会返回既有 user/assistant Message，并订阅同一条生成，不能再次执行模型。
	second := app.postAsync(app.conversationID, "client-1", "解释边界")
	waitForSubscription(t, app.hub)
	close(executor.release)

	firstResponse := waitForResponse(t, first, "first chat response")
	secondResponse := waitForResponse(t, second, "idempotent retry response")
	for _, response := range []*httptest.ResponseRecorder{firstResponse, secondResponse} {
		if response.Code != http.StatusOK {
			t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
		}
		if !strings.Contains(response.Body.String(), `"delta":"回答"`) {
			t.Fatalf("expected assistant delta in stream:\n%s", response.Body.String())
		}
	}
	if calls := executor.callCount(); calls != 1 {
		t.Fatalf("expected one executor call for idempotent retry, got %d", calls)
	}

	messages, err := app.messages.ListByConversation(t.Context(), app.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected one persisted user/assistant pair, got %+v", messages)
	}
	if messages[0].Role != conversation.MessageRoleUser || messages[1].Role != conversation.MessageRoleAssistant || messages[1].Status != conversation.MessageStatusCompleted || messages[1].Content != "回答" {
		t.Fatalf("unexpected persisted turn: %+v", messages)
	}
}

func TestChatHTTPIntegrationCancelPersistsTerminalMessage(t *testing.T) {
	executor := newControlledExecutor("", "")
	app := newChatHTTPIntegration(t, executor)
	stream := app.postAsync(app.conversationID, "client-1", "解释边界")
	waitForExecutorStart(t, executor, stream)
	waitForSubscription(t, app.hub)

	messages, err := app.messages.ListByConversation(t.Context(), app.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Status != conversation.MessageStatusStreaming {
		t.Fatalf("expected streaming assistant message, got %+v", messages)
	}
	cancelResponse := httptest.NewRecorder()
	cancelRequest := httptest.NewRequest(http.MethodPost, "/chat/"+messages[1].ID+"/cancel", nil)
	app.handler.ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", cancelResponse.Code, http.StatusOK)
	}

	streamResponse := waitForResponse(t, stream, "cancelled stream response")
	if !strings.Contains(streamResponse.Body.String(), `"type":"abort"`) {
		t.Fatalf("expected abort event:\n%s", streamResponse.Body.String())
	}
	message, err := app.messages.GetByID(t.Context(), messages[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Status != conversation.MessageStatusCancelled {
		t.Fatalf("assistant status = %q, want cancelled", message.Status)
	}
}

func TestChatHTTPIntegrationResumeStreamsSnapshotThenDelta(t *testing.T) {
	initial := strings.Repeat("初", 256) // 768 UTF-8 bytes: exceeds the 512-byte persistence buffer.
	executor := newControlledExecutor(initial, "后续")
	app := newChatHTTPIntegration(t, executor)
	first := app.postAsync(app.conversationID, "client-1", "解释边界")
	waitForExecutorStart(t, executor, first)
	waitForSubscription(t, app.hub)
	waitForSignal(t, executor.initialCommitted, "initial persisted chunk")

	messages, err := app.messages.ListByConversation(t.Context(), app.conversationID)
	if err != nil {
		t.Fatal(err)
	}
	assistantMessageID := messages[1].ID
	resumed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/chat/"+assistantMessageID+"/stream", nil)
		app.handler.ServeHTTP(response, request)
		resumed <- response
	}()
	waitForSubscription(t, app.hub)
	close(executor.release)

	_ = waitForResponse(t, first, "initial stream response")
	resumedResponse := waitForResponse(t, resumed, "resumed stream response")
	body := resumedResponse.Body.String()
	if !strings.Contains(body, `"delta":"`+initial+`"`) || !strings.Contains(body, `"delta":"后续"`) {
		t.Fatalf("expected snapshot followed by delta in resumed stream:\n%s", body)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForExecutorStart(t *testing.T, executor *controlledExecutor, response <-chan *httptest.ResponseRecorder) {
	t.Helper()
	select {
	case <-executor.started:
	case result := <-response:
		t.Fatalf("chat handler returned before executor start: status=%d body=%s", result.Code, result.Body.String())
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for executor start")
	}
}

func waitForSubscription(t *testing.T, hub *observingHub) {
	t.Helper()
	select {
	case <-hub.subscribed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream subscription")
	}
}

func waitForResponse(t *testing.T, response <-chan *httptest.ResponseRecorder, description string) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case result := <-response:
		return result
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}
