package chat

import (
	"context"
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

func TestMemoryGenerationRegistryCancelsOnce(t *testing.T) {
	registry := NewMemoryGenerationRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	if !registry.Register("m1", cancel) || registry.Register("m1", cancel) {
		t.Fatal("expected exactly one active registration")
	}
	done, active := registry.Cancel("m1")
	if !active {
		t.Fatal("expected active generation")
	}
	secondDone, active := registry.Cancel("m1")
	if !active || done != secondDone {
		t.Fatal("expected repeated cancellation to wait for the same active generation")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected cancelled context, got %v", ctx.Err())
	}
	select {
	case <-done:
		t.Fatal("done must remain open until generation cleanup completes")
	default:
	}

	registry.Complete("m1")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected completed generation to close done")
	}
	if done, active := registry.Cancel("m1"); active || done != nil {
		t.Fatal("expected completed generation to be absent")
	}
}

func TestDetachedRunContextIgnoresSourceCancellation(t *testing.T) {
	source, stopSource := context.WithCancel(context.Background())
	factory := NewDetachedRunContextFactory(source, time.Minute)
	runContext, stopRun := factory.New()
	stopSource()
	if runContext.Err() != nil {
		t.Fatalf("request cancellation must not cancel generation: %v", runContext.Err())
	}
	stopRun()
	if runContext.Err() != context.Canceled {
		t.Fatalf("explicit generation cancellation must win, got %v", runContext.Err())
	}
}
