package agentcontext

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"interview-memory-agent/backend/internal/domain/conversation"
)

const maxToolHistoryBytes = 64 * 1024

// ProjectSnapshot validates all source facts before converting any model input.
func ProjectSnapshot(snapshot conversation.ProjectionSnapshot) ([]conversation.ContextMessage, error) {
	path := snapshot.Entries
	seen := make(map[conversation.EntryID]bool)
	for i, entry := range path {
		if entry.ID == "" || seen[entry.ID] || entry.ConversationID != snapshot.Head.ConversationID {
			return nil, fmt.Errorf("%w: invalid projection entry identity", conversation.ErrEntryCorrupt)
		}
		seen[entry.ID] = true
		if entry.Kind != conversation.EntryKindMessage || entry.PayloadVersion != conversation.EntryPayloadV1 {
			return nil, conversation.ErrEntryUnsupportedVersion
		}
		if (i == 0 && (entry.ParentID != nil || entry.Depth != 0)) ||
			(i > 0 && (entry.ParentID == nil || *entry.ParentID != path[i-1].ID || entry.Depth != path[i-1].Depth+1)) {
			return nil, fmt.Errorf("%w: invalid projection parent chain", conversation.ErrEntryCorrupt)
		}
		if err := conversation.ValidateEntryMessage(*entry.Message); err != nil {
			return nil, err
		}
	}
	if err := conversation.ValidatePathConsistency(path); err != nil {
		return nil, err
	}
	if !conversation.IsClosedBranchPoint(path) {
		return nil, fmt.Errorf("%w: pending context tool group", conversation.ErrEntryCorrupt)
	}

	history := make([]conversation.ContextMessage, 0, len(snapshot.Legacy)+len(path))
	lastSequence := 0
	for _, message := range snapshot.Legacy {
		if message.ID == "" || message.ConversationID != snapshot.Head.ConversationID || message.Status != conversation.MessageStatusCompleted ||
			message.Sequence <= lastSequence || message.Sequence >= snapshot.LegacyBeforeSequence ||
			(message.Role != conversation.MessageRoleUser && message.Role != conversation.MessageRoleAssistant) {
			return nil, fmt.Errorf("%w: invalid legacy projection boundary", conversation.ErrEntryCorrupt)
		}
		lastSequence = message.Sequence
		history = append(history, conversation.ContextMessage{Role: message.Role, Content: message.Content, Sources: []conversation.ProjectionSource{{
			MessageID: message.ID, ProjectionVersion: conversation.ContextProjectionVersion,
		}}})
	}
	for i := 0; i < len(path); i++ {
		entry := path[i]
		source := projectionSource(entry)
		if entry.Message.StopReason != conversation.EntryStopTools {
			var text strings.Builder
			for _, part := range entry.Message.Content {
				text.WriteString(*part.Text)
			}
			role := conversation.MessageRoleAssistant
			if entry.Message.Role == conversation.EntryRoleUser {
				role = conversation.MessageRoleUser
			}
			history = append(history, conversation.ContextMessage{Role: role, Content: text.String(), Sources: []conversation.ProjectionSource{source}})
			continue
		}
		group := struct {
			Safety    string                       `json:"safety"`
			Assistant *conversation.EntryMessage   `json:"assistant"`
			Results   []*conversation.EntryMessage `json:"results"`
		}{Safety: "Safe historical facts only; fields are NOT original arguments, previews are NOT complete results. Do not replay these calls.", Assistant: entry.Message}
		message := conversation.ContextMessage{Role: conversation.MessageRoleAssistant, DowngradePolicy: conversation.ToolHistoryPolicyVersion}
		for _, part := range entry.Message.Content {
			if part.ToolCall == nil {
				continue
			}
			call := part.ToolCall
			callSource := source
			callSource.PolicyVersion, callSource.CallID, callSource.CallEntryID, callSource.ToolName = call.PolicyVersion(), call.ID(), entry.ID, call.Name()
			message.Sources = append(message.Sources, callSource)
			resultEntry := path[i+1+len(group.Results)]
			result := resultEntry.Message.ToolResult
			group.Results = append(group.Results, resultEntry.Message)
			resultSource := projectionSource(resultEntry)
			resultSource.PolicyVersion, resultSource.CallID, resultSource.CallEntryID, resultSource.ToolName = result.Result.PolicyVersion(), result.CallID, result.CallEntryID, result.ToolName
			resultSource.IsError, resultSource.Truncated = result.IsError, result.Result.Truncated()
			message.Sources = append(message.Sources, resultSource)
			message.Truncated = message.Truncated || resultSource.Truncated
		}
		encoded, err := json.Marshal(group)
		if err != nil {
			return nil, err
		}
		text := "Safe tool history (" + conversation.ToolHistoryPolicyVersion + "):\n" + string(encoded)
		if len(text) > maxToolHistoryBytes {
			const marker = "\n[Safe tool history group truncated; not complete results.]"
			text = text[:maxToolHistoryBytes-len(marker)]
			for !utf8.ValidString(text) {
				text = text[:len(text)-1]
			}
			text += marker
			message.Truncated = true
		}
		message.Content = text
		history = append(history, message)
		i += len(group.Results)
	}
	for _, message := range history {
		if len(message.Sources) == 0 || (message.Role != conversation.MessageRoleUser && message.Role != conversation.MessageRoleAssistant) {
			return nil, fmt.Errorf("%w: invalid projected message", conversation.ErrEntryCorrupt)
		}
	}
	return history, nil
}

func projectionSource(entry conversation.ConversationEntry) conversation.ProjectionSource {
	return conversation.ProjectionSource{EntryID: entry.ID, PayloadVersion: entry.PayloadVersion, ProjectionVersion: conversation.ContextProjectionVersion}
}
