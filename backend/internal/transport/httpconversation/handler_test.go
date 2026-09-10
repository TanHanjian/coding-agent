package httpconversation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	conversation "interview-memory-agent/backend/internal/domain/conversation"
)

type handlerService struct {
	conversation.Service
	createConversation func(context.Context, conversation.CreateConversationInput) (conversation.Conversation, error)
}

func (s handlerService) CreateConversation(ctx context.Context, input conversation.CreateConversationInput) (conversation.Conversation, error) {
	return s.createConversation(ctx, input)
}

func TestCreateConversationHandler(t *testing.T) {
	now := time.Now().UTC()
	handler := CreateConversationHandler(handlerService{createConversation: func(_ context.Context, input conversation.CreateConversationInput) (conversation.Conversation, error) {
		if input.Title != "复习计划" {
			t.Fatalf("unexpected input: %+v", input)
		}
		return conversation.Conversation{ID: "c1", Title: input.Title, CreatedAt: now, UpdatedAt: now}, nil
	}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/conversations", strings.NewReader(`{"title":"复习计划"}`)))
	if response.Code != http.StatusCreated {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusCreated)
	}
	var record conversation.Conversation
	if err := json.NewDecoder(response.Body).Decode(&record); err != nil {
		t.Fatal(err)
	}
	if record.ID != "c1" || record.Title != "复习计划" {
		t.Fatalf("unexpected response: %+v", record)
	}
}
