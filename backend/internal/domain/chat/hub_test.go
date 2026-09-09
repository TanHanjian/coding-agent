package chat

import (
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestMemoryGenerationHubSendsSnapshotThenDelta(t *testing.T) {
	hub := NewMemoryGenerationHub()
	hub.Open(conversation.Message{ID: "m1", Role: conversation.MessageRoleAssistant, Content: "已生成", Status: conversation.MessageStatusStreaming})
	subscription, ok := hub.Subscribe("m1")
	if !ok || subscription.Snapshot.Content != "已生成" || subscription.Snapshot.Status != conversation.MessageStatusStreaming {
		t.Fatalf("unexpected snapshot: %+v, ok=%v", subscription.Snapshot, ok)
	}
	hub.PublishText("m1", "你好")
	select {
	case got := <-subscription.Updates:
		if got.Kind != GenerationUpdateDelta || got.Text != "你好" {
			t.Fatalf("unexpected update: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected delta update")
	}
	hub.Complete(conversation.Message{ID: "m1", Role: conversation.MessageRoleAssistant, Content: "已生成你好", Status: conversation.MessageStatusCompleted})
	select {
	case got, ok := <-subscription.Updates:
		if !ok || got.Kind != GenerationUpdateTerminal || got.Status != conversation.MessageStatusCompleted {
			t.Fatalf("unexpected terminal update: %+v, open=%v", got, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("expected terminal update")
	}
	subscription.Close()
	subscription.Close()
	hub.PublishText("m1", "不应阻塞")
}
