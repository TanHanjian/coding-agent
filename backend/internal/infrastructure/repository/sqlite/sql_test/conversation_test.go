package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func TestSQLiteMessagesKeepCancelledAssistantContent(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "conversation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversationRepo := sqlite.NewConversationRepository(db)
	messageRepo := sqlite.NewMessageRepository(db)
	if err := conversationRepo.Create(ctx, conversation.Conversation{ID: "c1", Title: "取消测试", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	user, err := messageRepo.Create(ctx, conversation.Message{ID: "m-user", ConversationID: "c1", Role: conversation.MessageRoleUser, Content: "解释单调栈", Status: conversation.MessageStatusCompleted, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := messageRepo.Create(ctx, conversation.Message{ID: "m-assistant", ConversationID: "c1", Role: conversation.MessageRoleAssistant, Status: conversation.MessageStatusStreaming, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if user.Sequence != 1 || assistant.Sequence != 2 {
		t.Fatalf("unexpected message sequences: user=%d assistant=%d", user.Sequence, assistant.Sequence)
	}
	assistant.Content = "单调栈维护候选元素。"
	assistant.Status = conversation.MessageStatusCancelled
	assistant.UpdatedAt = now.Add(time.Second)
	if err := messageRepo.UpdateAssistant(ctx, assistant); err != nil {
		t.Fatal(err)
	}
	messages, err := messageRepo.ListByConversation(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Status != conversation.MessageStatusCancelled || messages[1].Content != assistant.Content {
		t.Fatalf("cancelled output was not preserved: %+v", messages)
	}
	assistant.Status = conversation.MessageStatusCompleted
	if err := messageRepo.UpdateAssistant(ctx, assistant); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("expected terminal message update to fail, got %v", err)
	}
	if err := conversationRepo.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if messages, err := messageRepo.ListByConversation(ctx, "c1"); err != nil || len(messages) != 0 {
		t.Fatalf("cascade delete failed: messages=%+v err=%v", messages, err)
	}
}
