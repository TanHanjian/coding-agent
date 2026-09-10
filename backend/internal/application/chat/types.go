package chat

import (
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// Request 是一次聊天生成所需的服务端可信输入。History 必须从 SQLite 加载，
// 调用方不得采用客户端传入的历史消息。
type Request struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	AssistantMessage conversation.Message
}

// RuntimeInput 是一次 Graph 运行所需的服务端可信输入。
type RuntimeInput struct {
	History          []*schema.Message
	Query            string
	InterviewContext string
}

// BuildInput 包含 Eino Agent 可使用的已持久化上下文。
type BuildInput struct {
	Conversation conversation.Conversation
	History      []conversation.Message
	UserMessage  conversation.Message
}

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
