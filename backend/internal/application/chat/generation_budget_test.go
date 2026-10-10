package chat

import (
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestGenerationHubFailureCodeReachesAllSubscribers(t *testing.T) {
	for _, owned := range []bool{false, true} {
		for _, code := range []string{ContextBudgetExceededCode, ContextCountingFailedCode, "model_failed"} {
			t.Run(code+map[bool]string{false: "/open", true: "/lease"}[owned], func(t *testing.T) {
				hub := NewMemoryGenerationHub()
				message := conversation.Message{ID: "a1", Role: conversation.MessageRoleAssistant, Status: conversation.MessageStatusStreaming, Content: "partial"}
				complete := hub.Complete
				if owned {
					lease, ok := hub.Claim(message)
					if !ok {
						t.Fatal("claim failed")
					}
					t.Cleanup(lease.Release)
					complete = lease.Complete
				} else {
					hub.Open(message)
				}
				first, ok := hub.Subscribe(message.ID)
				if !ok {
					t.Fatal("initial subscribe failed")
				}
				t.Cleanup(first.Close)
				hub.PublishText(message.ID, " tail")
				resumed, ok := hub.Subscribe(message.ID)
				if !ok || resumed.Snapshot.Content != "partial tail" || resumed.Snapshot.ErrorCode != "" {
					t.Fatalf("active reconnect: %+v", resumed.Snapshot)
				}
				t.Cleanup(resumed.Close)
				message.Status, message.ErrorCode, message.ErrorMessage = conversation.MessageStatusFailed, code, "private provider diagnostic"
				message.Content = "partial tail"
				complete(message)
				for _, sub := range []GenerationSubscription{first, resumed} {
					terminals := 0
				loop:
					for {
						select {
						case update, open := <-sub.Updates:
							if !open {
								break loop
							}
							if update.Kind == GenerationUpdateTerminal {
								terminals++
								if update.Status != message.Status || update.ErrorCode != code {
									t.Fatalf("terminal = %+v", update)
								}
							}
						case <-time.After(time.Second):
							t.Fatal("subscription did not close")
						}
					}
					if terminals != 1 {
						t.Fatalf("terminal count = %d", terminals)
					}
				}
			})
		}
	}
}
