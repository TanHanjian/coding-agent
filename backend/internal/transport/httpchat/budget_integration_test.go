package httpchat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

type budgetExecutor func(context.Context, chat.Request, chat.TextSink) error

func (f budgetExecutor) Stream(ctx context.Context, request chat.Request, sink chat.TextSink) error {
	return f(ctx, request, sink)
}

func TestRunBudgetHTTPIntegrationLiveReconnectAndHistory(t *testing.T) {
	for _, tc := range []struct {
		sentinel error
		code     string
	}{
		{chat.ErrContextBudgetExceeded, chat.ContextBudgetExceededCode},
		{chat.ErrContextCountingFailed, chat.ContextCountingFailedCode},
	} {
		t.Run(tc.code, func(t *testing.T) {
			db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "budget.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := storage.Migrate(t.Context(), db); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if err := sqlite.NewConversationRepository(db).Create(t.Context(), conversation.Conversation{ID: "c1", Title: "budget", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			repo, err := sqlite.NewAgentRunRepository(db)
			if err != nil {
				t.Fatal(err)
			}
			hub := newObservingHub()
			release := make(chan struct{})
			root, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			executor := budgetExecutor(func(ctx context.Context, request chat.Request, sink chat.TextSink) error {
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				if err := sink.WriteChunk(ctx, "已经生成的部分回答"); err != nil {
					return err
				}
				text := "cached final must be discarded"
				if _, err := request.Recorder.RecordAssistant(ctx, conversation.EntryMessage{Role: conversation.EntryRoleAssistant, StopReason: conversation.EntryStopNormal, Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}}}); err != nil {
					return err
				}
				return fmt.Errorf("Graph stream reader: %w: private tool arguments and provider prompt", tc.sentinel)
			})
			service, err := chat.NewRunService(chat.RunDependencies{Executor: executor, Store: repo, Hub: hub, Registry: chat.NewMemoryGenerationRegistry(), RunContexts: chat.NewDetachedRunContextFactory(root, time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			router := chi.NewRouter()
			RegisterRoutes(router, service)
			app := &chatHTTPIntegration{handler: router, conversationID: "c1", hub: hub, messages: sqlite.NewMessageRepository(db)}
			first := app.postAsync("c1", "u1", "question")
			waitForSubscription(t, hub)
			close(release)
			live := waitForResponse(t, first, "budget stream response")
			id := live.Header().Get("X-Assistant-Message-ID")
			message, err := app.messages.GetByID(t.Context(), id)
			if err != nil || message.Status != conversation.MessageStatusFailed || message.ErrorCode != tc.code || message.ErrorMessage != chat.SafeGenerationErrorText(tc.code) || message.Content != "已经生成的部分回答" {
				t.Fatalf("history = %+v, %v", message, err)
			}
			reconnect := httptest.NewRecorder()
			router.ServeHTTP(reconnect, httptest.NewRequest(http.MethodGet, "/chat/"+id+"/stream", nil))
			for _, response := range []*httptest.ResponseRecorder{live, reconnect} {
				body := response.Body.String()
				if response.Code != http.StatusOK || !strings.Contains(body, chat.SafeGenerationErrorText(tc.code)) || !strings.Contains(body, message.Content) || strings.Contains(body, "private") || strings.Contains(body, "cached final") || strings.Contains(body, `"type":"finish"`) {
					t.Fatalf("unsafe/inconsistent response: %d %s", response.Code, body)
				}
			}
			if live.Body.String() != reconnect.Body.String() {
				t.Fatalf("live and terminal reconnect differ:\nlive=%s\nreconnect=%s", live.Body.String(), reconnect.Body.String())
			}
		})
	}
}
