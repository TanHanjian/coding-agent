package chat

import (
	"context"
	"testing"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestOwnedGenerationLeasesProtectReplacementAndLegacyCleanup(t *testing.T) {
	hub := NewMemoryGenerationHub()
	registry := NewMemoryGenerationRegistry()
	message := conversation.Message{ID: "owned", Role: conversation.MessageRoleAssistant, Status: conversation.MessageStatusStreaming, Content: "checkpoint"}
	lease, ok := hub.Claim(message)
	if !ok {
		t.Fatal("initial hub claim")
	}
	cancelCount := 0
	control, ok := registry.Claim(message.ID, func() { cancelCount++ })
	if !ok {
		t.Fatal("initial registry claim")
	}
	if _, ok := hub.Claim(message); ok {
		t.Fatal("duplicate hub claim")
	}
	if _, ok := registry.Claim(message.ID, func() { t.Error("foreign cancel") }); ok {
		t.Fatal("duplicate registry claim")
	}
	subscription, ok := hub.Subscribe(message.ID)
	if !ok {
		t.Fatal("subscribe")
	}
	terminal := message
	terminal.Status = conversation.MessageStatusCompleted
	hub.Complete(terminal)
	registry.Complete(message.ID)
	select {
	case <-subscription.Updates:
		t.Fatal("legacy cleanup affected lease")
	default:
	}
	done, active := registry.Cancel(message.ID)
	if !active || cancelCount != 1 {
		t.Fatal("cancel lost owned execution")
	}
	lease.Release()
	control.Release()
	select {
	case <-done:
	default:
		t.Fatal("release did not finish")
	}
	select {
	case _, ok := <-subscription.Updates:
		if ok {
			t.Fatal("release invented terminal")
		}
	default:
		t.Fatal("release did not close subscription")
	}
	replacement, ok := hub.Claim(message)
	if !ok {
		t.Fatal("replacement claim")
	}
	replacementControl, ok := registry.Claim(message.ID, func() {})
	if !ok {
		t.Fatal("replacement registry")
	}
	lease.Complete(terminal)
	control.Release()
	if sub, ok := hub.Subscribe(message.ID); !ok {
		t.Fatal("stale lease deleted replacement")
	} else {
		sub.Close()
	}
	if _, ok := registry.Cancel(message.ID); !ok {
		t.Fatal("stale lease removed replacement control")
	}
	replacement.Complete(terminal)
	replacementControl.Release()
}

func TestOwnedHubLeasePublishesOnlyItsCommittedTerminal(t *testing.T) {
	hub := NewMemoryGenerationHub()
	message := conversation.Message{ID: "owned", Role: conversation.MessageRoleAssistant, Status: conversation.MessageStatusStreaming}
	lease, _ := hub.Claim(message)
	sub, _ := hub.Subscribe(message.ID)
	wrong := message
	wrong.ID = "other"
	wrong.Status = conversation.MessageStatusFailed
	lease.Complete(wrong)
	terminal := message
	terminal.Status = conversation.MessageStatusCompleted
	lease.Complete(terminal)
	update, ok := <-sub.Updates
	if !ok || update.Kind != GenerationUpdateTerminal || update.Status != terminal.Status {
		t.Fatal("missing terminal")
	}
	if _, ok := <-sub.Updates; ok {
		t.Fatal("subscription not closed")
	}
	lease.Release()
}

func TestMemoryRegistryLeaseRequiresValidInput(t *testing.T) {
	registry := NewMemoryGenerationRegistry()
	if _, ok := registry.Claim("", func() {}); ok {
		t.Fatal("empty ID accepted")
	}
	if _, ok := registry.Claim("id", nil); ok {
		t.Fatal("nil cancel accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	lease, ok := registry.Claim("id", cancel)
	if !ok {
		t.Fatal("claim")
	}
	registry.Cancel("id")
	if ctx.Err() != context.Canceled {
		t.Fatal("cancel not propagated")
	}
	lease.Release()
}
