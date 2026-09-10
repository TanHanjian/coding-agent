package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func TestSQLiteConversationDeleteCascadesChatMessages(t *testing.T) {
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
	chatRepo := sqlite.NewChatTurnRepository(db)
	turn, err := chatRepo.BeginTurn(ctx, chat.BeginTurnInput{
		ConversationID:  "c1",
		ClientMessageID: "client-message-1",
		Text:            "解释单调栈",
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.UserMessage.Sequence != 1 || turn.AssistantMessage.Sequence != 2 {
		t.Fatalf("unexpected message sequences: user=%d assistant=%d", turn.UserMessage.Sequence, turn.AssistantMessage.Sequence)
	}
	if err := chatRepo.AppendAssistantText(ctx, turn.AssistantMessage.ID, "单调栈维护候选元素。"); err != nil {
		t.Fatal(err)
	}
	if _, err := chatRepo.FinishAssistant(ctx, chat.FinishAssistantInput{
		AssistantMessageID: turn.AssistantMessage.ID,
		Status:             conversation.MessageStatusCancelled,
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := messageRepo.ListByConversation(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Status != conversation.MessageStatusCancelled || messages[1].Content != "单调栈维护候选元素。" {
		t.Fatalf("cancelled output was not preserved: %+v", messages)
	}
	if err := conversationRepo.Delete(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if messages, err := messageRepo.ListByConversation(ctx, "c1"); err != nil || len(messages) != 0 {
		t.Fatalf("cascade delete failed: messages=%+v err=%v", messages, err)
	}
}
