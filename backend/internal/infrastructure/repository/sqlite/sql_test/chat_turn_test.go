package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

func TestChatTurnRepositoryBeginTurnCreatesAndReusesMessagePair(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "chat-turn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	conversationRepo := sqlite.NewConversationRepository(db)
	if err := conversationRepo.Create(ctx, conversation.Conversation{ID: "c1", Title: "测试", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.NewChatTurnRepository(db)
	first, err := repo.BeginTurn(ctx, chat.BeginTurnInput{ConversationID: "c1", ClientMessageID: "client-1", Text: "解释单调栈"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused || first.UserMessage.Sequence != 1 || first.AssistantMessage.Sequence != 2 || first.AssistantMessage.Status != conversation.MessageStatusStreaming {
		t.Fatalf("unexpected first turn: %+v", first)
	}
	if err := repo.AppendAssistantText(ctx, first.AssistantMessage.ID, "单调栈"); err != nil {
		t.Fatal(err)
	}
	if err := repo.AppendAssistantText(ctx, first.AssistantMessage.ID, "维护候选元素"); err != nil {
		t.Fatal(err)
	}
	messageRepo := sqlite.NewMessageRepository(db)
	persistedAssistant, err := messageRepo.GetByID(ctx, first.AssistantMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persistedAssistant.Content != "单调栈维护候选元素" {
		t.Fatalf("unexpected assistant content %q", persistedAssistant.Content)
	}
	reused, err := repo.BeginTurn(ctx, chat.BeginTurnInput{ConversationID: "c1", ClientMessageID: "client-1", Text: "不应创建第二次"})
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Reused || reused.UserMessage.ID != first.UserMessage.ID || reused.AssistantMessage.ID != first.AssistantMessage.ID {
		t.Fatalf("expected reused message pair, got %+v", reused)
	}
	if _, err := repo.BeginTurn(ctx, chat.BeginTurnInput{ConversationID: "c1", ClientMessageID: "client-2", Text: "并发生成"}); err != domainerr.ErrConflict {
		t.Fatalf("expected active-generation conflict, got %v", err)
	}
	finished, err := repo.FinishAssistant(ctx, chat.FinishAssistantInput{
		AssistantMessageID: first.AssistantMessage.ID,
		Status:             conversation.MessageStatusCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != conversation.MessageStatusCompleted || finished.Content != "单调栈维护候选元素" {
		t.Fatalf("unexpected finished message: %+v", finished)
	}
	loaded, err := repo.GetMessage(ctx, first.AssistantMessage.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != conversation.MessageStatusCompleted {
		t.Fatalf("unexpected loaded message: %+v", loaded)
	}
	if _, err := repo.FinishAssistant(ctx, chat.FinishAssistantInput{
		AssistantMessageID: first.AssistantMessage.ID,
		Status:             conversation.MessageStatusCompleted,
	}); err != domainerr.ErrConflict {
		t.Fatalf("expected completed message finish conflict, got %v", err)
	}
}
