package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

type handlerService struct {
	Service
	start  func(context.Context, StartInput) (StartResult, error)
	cancel func(context.Context, string) (conversation.Message, error)
}

func (s handlerService) Start(ctx context.Context, input StartInput) (StartResult, error) {
	return s.start(ctx, input)
}

func (s handlerService) Cancel(ctx context.Context, id string) (conversation.Message, error) {
	return s.cancel(ctx, id)
}

func TestStartHandlerPassesLatestUserMessage(t *testing.T) {
	handler := StartHandler(handlerService{start: func(_ context.Context, input StartInput) (StartResult, error) {
		if input.ConversationID != "c1" || input.ClientMessageID != "client-1" || input.Text != "解释边界" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return StartResult{}, domainerr.ErrNotImplemented
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"client-1","role":"user","parts":[{"type":"text","text":"解释边界"}]}}`)))
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNotImplemented)
	}
}

func TestStartHandlerRejectsNonTextPart(t *testing.T) {
	handler := StartHandler(handlerService{start: func(context.Context, StartInput) (StartResult, error) {
		t.Fatal("service must not be called")
		return StartResult{}, nil
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"id":"c1","message":{"id":"client-1","role":"user","parts":[{"type":"file","text":"x"}]}}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusBadRequest)
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
