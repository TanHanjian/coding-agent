package httpchat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

type handlerService struct {
	chat.Service
	start     func(context.Context, chat.StartInput) (chat.StartResult, error)
	subscribe func(context.Context, string) (chat.GenerationSubscription, error)
	cancel    func(context.Context, string) (conversation.Message, error)
}

func (s handlerService) Start(ctx context.Context, input chat.StartInput) (chat.StartResult, error) {
	return s.start(ctx, input)
}

func (s handlerService) Subscribe(ctx context.Context, id string) (chat.GenerationSubscription, error) {
	return s.subscribe(ctx, id)
}

func (s handlerService) Cancel(ctx context.Context, id string) (conversation.Message, error) {
	return s.cancel(ctx, id)
}

func TestStartHandlerPassesLatestUserMessage(t *testing.T) {
	handler := StartHandler(handlerService{start: func(_ context.Context, input chat.StartInput) (chat.StartResult, error) {
		if input.ConversationID != "c1" || input.ClientMessageID != "client-1" || input.Text != "解释边界" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return chat.StartResult{}, domainerr.ErrNotImplemented
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"client-1","role":"user","parts":[{"type":"text","text":"解释边界"}]}}`)))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNotImplemented)
	}
}

func TestStartHandlerRejectsNonTextPart(t *testing.T) {
	handler := StartHandler(handlerService{start: func(context.Context, chat.StartInput) (chat.StartResult, error) {
		t.Fatal("service must not be called")
		return chat.StartResult{}, nil
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"client-1","role":"user","parts":[{"type":"file","text":"x"}]}}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestStartHandlerStreamsSnapshotAndUpdates(t *testing.T) {
	updates := make(chan chat.GenerationUpdate, 2)
	updates <- chat.GenerationUpdate{Kind: chat.GenerationUpdateDelta, Text: "续写"}
	updates <- chat.GenerationUpdate{Kind: chat.GenerationUpdateTerminal, Status: conversation.MessageStatusCompleted}
	close(updates)
	handler := StartHandler(handlerService{
		start: func(_ context.Context, input chat.StartInput) (chat.StartResult, error) {
			return chat.StartResult{ConversationID: input.ConversationID, UserMessageID: "user-1", AssistantMessageID: "assistant-1"}, nil
		},
		subscribe: func(_ context.Context, id string) (chat.GenerationSubscription, error) {
			if id != "assistant-1" {
				t.Fatalf("unexpected assistant message id %q", id)
			}
			return chat.GenerationSubscription{
				Snapshot: chat.GenerationSnapshot{AssistantMessageID: id, Content: "已有", Status: conversation.MessageStatusStreaming},
				Updates:  updates,
			}, nil
		},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"client-1","role":"user","parts":[{"type":"text","text":"解释边界"}]}}`)))

	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := response.Header().Get("x-vercel-ai-ui-message-stream"); got != "v1" {
		t.Fatalf("unexpected stream version %q", got)
	}
	if got := response.Header().Get("X-Assistant-Message-ID"); got != "assistant-1" {
		t.Fatalf("unexpected assistant message header %q", got)
	}
	body := response.Body.String()
	for _, expected := range []string{
		`data: {"messageId":"assistant-1","type":"start"}`,
		`data: {"id":"assistant-1-text","type":"text-start"}`,
		`data: {"delta":"已有","id":"assistant-1-text","type":"text-delta"}`,
		`data: {"delta":"续写","id":"assistant-1-text","type":"text-delta"}`,
		`data: {"id":"assistant-1-text","type":"text-end"}`,
		`data: {"type":"finish"}`,
		"data: [DONE]",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream body missing %q:\n%s", expected, body)
		}
	}
}

func TestCancelHandlerPassesAssistantMessageID(t *testing.T) {
	handler := CancelHandler(handlerService{cancel: func(_ context.Context, id string) (conversation.Message, error) {
		if id != "m1" {
			t.Fatalf("unexpected id %q", id)
		}
		return conversation.Message{ID: id, Status: conversation.MessageStatusCancelled}, nil
	}})
	request := httptest.NewRequest(http.MethodPost, "/chat/m1/cancel", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("assistantMessageID", "m1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)))
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
}
