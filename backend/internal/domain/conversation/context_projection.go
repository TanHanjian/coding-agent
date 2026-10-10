package conversation

import "context"

const ContextProjectionVersion = "context-v1"
const ToolHistoryPolicyVersion = "safe-tool-facts-v1"

// ProjectionQuery binds a read to the current persisted turn, never a client history.
type ProjectionQuery struct {
	ConversationID string
	RunID          string
	UserMessageID  string
}

// ProjectionSnapshot is loaded in one transaction. LegacyBeforeSequence is
// the first tree-backed user Message sequence; dual-written Messages are excluded.
type ProjectionSnapshot struct {
	Head                 EntryHead
	Entries              []ConversationEntry
	Legacy               []Message
	LegacyBeforeSequence int
}

type ProjectionSource struct {
	EntryID           EntryID
	MessageID         string
	PayloadVersion    int
	ProjectionVersion string
	PolicyVersion     string
	CallID            string
	CallEntryID       EntryID
	ToolName          string
	Truncated         bool
	IsError           bool
}

// ContextMessage is provider-neutral. Tool history is approved factual text,
// never reconstructed executable arguments or a fabricated tool response.
type ContextMessage struct {
	Role            MessageRole
	Content         string
	Sources         []ProjectionSource
	DowngradePolicy string
	Truncated       bool
}

func CloneContextMessages(messages []ContextMessage) []ContextMessage {
	if messages == nil {
		return nil
	}
	result := make([]ContextMessage, len(messages))
	for i, message := range messages {
		result[i] = message
		result[i].Sources = append([]ProjectionSource(nil), message.Sources...)
	}
	return result
}

type ProjectionReader interface {
	ReadProjection(context.Context, ProjectionQuery) (ProjectionSnapshot, error)
}
