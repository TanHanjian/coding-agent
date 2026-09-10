package chat

import "interview-memory-agent/backend/internal/domain/conversation"

type BeginTurnInput struct {
	ConversationID  string
	ClientMessageID string
	Text            string
}

type BeginTurnResult struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	AssistantMessage conversation.Message
	Reused           bool
}

type FinishAssistantInput struct {
	AssistantMessageID string
	Status             conversation.MessageStatus
	ErrorCode          string
	ErrorMessage       string
}
