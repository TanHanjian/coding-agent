package agentcontext

import (
	"context"
	"testing"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestManagerPreservesCurrentPassThroughBehavior(t *testing.T) {
	manager := NewManager()
	prepared, err := manager.Prepare(context.Background(), chat.ContextInput{
		History: []conversation.Message{{
			Role:    conversation.MessageRoleUser,
			Content: "历史问题",
		}},
		UserMessage: conversation.Message{
			Role:    conversation.MessageRoleUser,
			Content: "当前问题",
		},
		InterviewContext: "当前材料",
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.Query != "当前问题" || prepared.InterviewContext != "当前材料" {
		t.Fatalf("prepared context = %#v", prepared)
	}
	if len(prepared.History) != 1 || prepared.History[0].Content != "历史问题" {
		t.Fatalf("prepared history = %#v", prepared.History)
	}
}
