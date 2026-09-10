// Package conversation defines persisted user-visible chat records.
package conversation

import "time"

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

type MessageStatus string

const (
	MessageStatusStreaming MessageStatus = "streaming"
	MessageStatusCompleted MessageStatus = "completed"
	MessageStatusCancelled MessageStatus = "cancelled"
	MessageStatusFailed    MessageStatus = "failed"
)

type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Message is a user-visible conversation entry. It does not retain Eino's
// internal Turns, tool calls, or reasoning.
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	// ClientMessageID 仅用于聊天请求幂等，不属于对外的历史消息 DTO。
	ClientMessageID string        `json:"-"`
	Sequence        int           `json:"sequence"`
	Role            MessageRole   `json:"role"`
	Content         string        `json:"content"`
	Status          MessageStatus `json:"status"`
	ErrorCode       string        `json:"errorCode,omitempty"`
	ErrorMessage    string        `json:"errorMessage,omitempty"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
}

type CreateConversationInput struct {
	Title string `json:"title"`
}

type UpdateConversationInput struct {
	Title *string `json:"title"`
}
