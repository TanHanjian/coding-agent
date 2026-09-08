// Package chat 定义 HTTP 流式响应和基于 Eino 的 Agent 运行时之间的应用边界。
package chat

import (
	"context"

	"github.com/cloudwego/eino/adk"
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

// AgentBuilder 是组装应用专属 Eino Agent 的扩展点。提示词、模型选择、工具白名单
// 和检索策略有意留给调用方实现。
type AgentBuilder interface {
	Build(context.Context, BuildInput) (adk.Agent, error)
}

// BuildInput 包含 Eino Agent 可使用的已持久化上下文。
type BuildInput struct {
	Conversation conversation.Conversation
	History      []conversation.Message
	UserMessage  conversation.Message
}
