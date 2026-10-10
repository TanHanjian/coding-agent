package chat

import (
	"testing"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestContextMessagesToSchemaPreservesSourcesWithoutProtocol(t *testing.T) {
	messages := []conversation.ContextMessage{
		{Role: conversation.MessageRoleUser, Content: "legacy", Sources: []conversation.ProjectionSource{{MessageID: "m1", ProjectionVersion: conversation.ContextProjectionVersion}}},
		{Role: conversation.MessageRoleAssistant, Content: "safe facts", DowngradePolicy: conversation.ToolHistoryPolicyVersion, Truncated: true, Sources: []conversation.ProjectionSource{{EntryID: "e1", PayloadVersion: 1, ProjectionVersion: conversation.ContextProjectionVersion, PolicyVersion: "v1", CallID: "c1", CallEntryID: "e0", ToolName: "search", IsError: true, Truncated: true}}},
	}
	history, err := ContextMessagesToSchema(messages)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Role != schema.User || history[1].Role != schema.Assistant || history[1].Extra["context_downgrade_policy"] != conversation.ToolHistoryPolicyVersion || history[1].Extra["context_truncated"] != true {
		t.Fatalf("history = %+v", history)
	}
	for _, message := range history {
		if len(message.ToolCalls) != 0 || message.ToolCallID != "" {
			t.Fatalf("executable history = %+v", message)
		}
	}
	sources := history[1].Extra["context_sources"].([]conversation.ProjectionSource)
	if sources[0] != messages[1].Sources[0] {
		t.Fatalf("lost provenance = %+v", sources)
	}
	sources[0].CallID = "changed"
	if messages[1].Sources[0].CallID != "c1" {
		t.Fatal("source metadata aliases input")
	}
	empty, err := ContextMessagesToSchema([]conversation.ContextMessage{})
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty = %#v %v", empty, err)
	}
}

func TestContextMessagesToSchemaRejectsInvalidBoundary(t *testing.T) {
	for _, name := range []string{"missing provenance", "unknown role", "unknown projection", "unknown payload", "missing identity", "ambiguous identity", "unknown downgrade"} {
		t.Run(name, func(t *testing.T) {
			message := conversation.ContextMessage{Role: conversation.MessageRoleAssistant, Content: "facts", Sources: []conversation.ProjectionSource{{EntryID: "e1", PayloadVersion: 1, ProjectionVersion: conversation.ContextProjectionVersion}}}
			switch name {
			case "missing provenance":
				message.Sources = nil
			case "unknown role":
				message.Role = "tool"
			case "unknown projection":
				message.Sources[0].ProjectionVersion = "future"
			case "unknown payload":
				message.Sources[0].PayloadVersion = 99
			case "missing identity":
				message.Sources[0].EntryID = ""
			case "ambiguous identity":
				message.Sources[0].MessageID = "m1"
			case "unknown downgrade":
				message.DowngradePolicy = "raw-tool-replay"
			}
			history, err := ContextMessagesToSchema([]conversation.ContextMessage{message})
			if err == nil || history != nil {
				t.Fatalf("history = %+v, err = %v", history, err)
			}
		})
	}
}
