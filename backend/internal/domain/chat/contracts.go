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

// TextSink 接收用户可见的助手文本。Chat 服务将提供同时批量持久化并编码
// AI SDK 数据流的实现。
type TextSink interface {
	AppendText(context.Context, string) error
}

// Executor 是 Chat 应用层唯一需要的 Eino 依赖。实现必须在 ctx 取消后及时停止。
type Executor interface {
	Stream(context.Context, Request, TextSink) error
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
