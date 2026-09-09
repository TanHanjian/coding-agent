// Package chat 定义 HTTP 流式响应和基于 Eino 的 Agent 运行时之间的应用边界。
package chat

import (
	"context"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// Request 是一次聊天生成所需的、由服务端信任的输入。History 必须从 SQLite
// 加载，调用方不得采用客户端传入的历史消息。
type Request struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	AssistantMessage conversation.Message
}

// TextSink 接收用户可见的助手文本。它属于生成运行本身，而不是某一个 HTTP
// 响应；实现应批量持久化文本并向活跃订阅者发布 delta。
type TextSink interface {
	WriteChunk(context.Context, string) error
}

// GenerationSnapshot 是订阅建立瞬间的完整可见状态。前端必须先用它替换本地
// assistant 内容，再持续追加后续 delta，因此完整刷新无需 afterSequence。
type GenerationSnapshot struct {
	AssistantMessageID string
	Content            string
	Status             conversation.MessageStatus
}

type GenerationUpdate struct {
	Kind   GenerationUpdateKind
	Text   string
	Status conversation.MessageStatus
}

const (
	GenerationUpdateDelta    GenerationUpdateKind = "delta"
	GenerationUpdateTerminal GenerationUpdateKind = "terminal"
)

type GenerationUpdateKind string

// GenerationHub 是单进程内仍在运行的生成任务及其订阅者集合。它不持久化
// 事件日志：刷新后的客户端由 Snapshot 校正文本，再接收之后的 Delta。
type GenerationHub interface {
	Open(conversation.Message)
	PublishText(assistantMessageID, text string)
	Complete(conversation.Message)
	Subscribe(assistantMessageID string) (GenerationSubscription, bool)
}

type GenerationSubscription struct {
	Snapshot GenerationSnapshot
	Updates  <-chan GenerationUpdate
	close    func()
}

func (s GenerationSubscription) Close() {
	if s.close != nil {
		s.close()
	}
}

// Executor 是 Chat 应用层唯一需要的 Eino 依赖。实现必须在 ctx 取消后及时停止。
type Executor interface {
	Stream(context.Context, Request, TextSink) error
}

// TurnStore 是一次聊天生成所需的持久化边界。它和 Conversation 的普通 CRUD
// 分开，是因为这里需要原子性、幂等性和流式文本追加；具体 SQLite 事务由后续实现。
type TurnStore interface {
	// BeginTurn 原子创建或复用由 client_message_id 标识的 user Message，
	// 并创建 streaming assistant Message。重试时不得重复创建消息对。
	BeginTurn(context.Context, BeginTurnInput) (BeginTurnResult, error)
	// AppendAssistantText 将一批文本追加到仍处于 streaming 状态的 assistant Message。
	AppendAssistantText(context.Context, string, string) error
	// FinishAssistant 将 streaming Message 变为唯一允许的终态之一，并保留已累积文本。
	FinishAssistant(context.Context, FinishAssistantInput) (conversation.Message, error)
	GetMessage(context.Context, string) (conversation.Message, error)
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
	// Reused 表示同一个客户端幂等键已经创建过这次消息对。
	Reused bool
}

type FinishAssistantInput struct {
	AssistantMessageID string
	Status             conversation.MessageStatus
	ErrorCode          string
	ErrorMessage       string
}

// RuntimeInput 是一次 Graph 运行所需的服务端可信输入。它由 EinoExecutor
// 根据 SQLite 的持久化记录创建，前端不能直接构造或传入。
type RuntimeInput struct {
	// History 是已完成的历史消息，不包含当前用户问题。
	History []*schema.Message

	// Query 是当前用户显式发送的文本。
	Query string

	// InterviewContext 是服务端加载并整理的题目、回答和已有复盘摘要。首版可为空。
	InterviewContext string
}

// Runtime 是 Chat 应用层对 Eino 可流式运行单元的最小依赖。compose.Runnable
// 能直接满足该接口；应用层因此不需要关心运行单元来自 ADK Agent 还是 Graph。
type Runtime interface {
	Stream(context.Context, RuntimeInput, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

// RuntimeBuilder 是组装应用专属 Eino 运行时的扩展点。提示词、模型选择、Graph
// 节点、条件分支、工具白名单和检索策略有意留给调用方实现。
type RuntimeBuilder interface {
	Build(context.Context, BuildInput) (Runtime, error)
}

// BuildInput 包含 Eino Agent 可使用的已持久化上下文。
type BuildInput struct {
	Conversation conversation.Conversation
	History      []conversation.Message
	UserMessage  conversation.Message
}
