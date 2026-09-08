package conversation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type handlerService struct {
	Service
	createConversation func(context.Context, CreateConversationInput) (Conversation, error)
	updateAssistant    func(context.Context, string, UpdateAssistantMessageInput) (Message, error)
}

func (s handlerService) CreateConversation(ctx context.Context, input CreateConversationInput) (Conversation, error) {
	return s.createConversation(ctx, input)
}

func (s handlerService) UpdateAssistantMessage(ctx context.Context, id string, input UpdateAssistantMessageInput) (Message, error) {
	return s.updateAssistant(ctx, id, input)
}

func TestCreateConversationHandler(t *testing.T) {
	now := time.Now().UTC()
	handler := CreateConversationHandler(handlerService{createConversation: func(_ context.Context, input CreateConversationInput) (Conversation, error) {
		if input.Title != "复习计划" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return Conversation{ID: "c1", Title: input.Title, CreatedAt: now, UpdatedAt: now}, nil
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/conversations", strings.NewReader(`{"title":"复习计划"}`)))
	if response.Code != http.StatusCreated {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusCreated)
	}
	var record Conversation
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.ID != "c1" || record.Title != "复习计划" {
		t.Fatalf("unexpected response: %+v", record)
	}
}

func TestUpdateAssistantMessageHandler(t *testing.T) {
	content := "已输出的内容"
	handler := UpdateAssistantMessageHandler(handlerService{updateAssistant: func(_ context.Context, id string, input UpdateAssistantMessageInput) (Message, error) {
		if id != "m1" || input.Content == nil || *input.Content != content || input.Status != MessageStatusCancelled {
			t.Fatalf("unexpected update: id=%q input=%+v", id, input)
		}
		return Message{ID: id, ConversationID: "c1", Role: MessageRoleAssistant, Content: *input.Content, Status: input.Status}, nil
	}})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/messages/m1", strings.NewReader(`{"content":"已输出的内容","status":"cancelled"}`))
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("messageID", "m1")
	handler.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext)))
	if response.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusOK)
	}
}
