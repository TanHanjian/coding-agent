package chat

import (
	"fmt"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// ContextMessagesToSchema validates the provider boundary and keeps provenance
// internal. Safe historical tools are text only, never executable protocol.
func ContextMessagesToSchema(messages []conversation.ContextMessage) ([]*schema.Message, error) {
	history := make([]*schema.Message, 0, len(messages))
	for i, message := range messages {
		if len(message.Sources) == 0 {
			return nil, fmt.Errorf("context history message %d: missing provenance", i)
		}
		if message.DowngradePolicy != "" && message.DowngradePolicy != conversation.ToolHistoryPolicyVersion {
			return nil, fmt.Errorf("context history message %d: unsupported downgrade policy", i)
		}
		for _, source := range message.Sources {
			if source.ProjectionVersion != conversation.ContextProjectionVersion ||
				(source.EntryID == "") == (source.MessageID == "") ||
				(source.EntryID != "" && source.PayloadVersion != conversation.EntryPayloadV1) {
				return nil, fmt.Errorf("context history message %d: invalid source version or identity", i)
			}
		}
		var role schema.RoleType
		switch message.Role {
		case conversation.MessageRoleUser:
			role = schema.User
		case conversation.MessageRoleAssistant:
			role = schema.Assistant
		default:
			return nil, fmt.Errorf("context history message %d: unsupported role %q", i, message.Role)
		}
		copy := conversation.CloneContextMessages([]conversation.ContextMessage{message})[0]
		history = append(history, &schema.Message{Role: role, Content: message.Content, Extra: map[string]any{
			"context_sources":          copy.Sources,
			"context_downgrade_policy": message.DowngradePolicy,
			"context_truncated":        message.Truncated,
		}})
	}
	return history, nil
}
