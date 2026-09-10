package chat

import (
	"context"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// TextSink 接收用户可见的助手文本。实现应批量持久化文本并向活跃订阅者发布 delta。
type TextSink interface {
	WriteChunk(context.Context, string) error
}

// Executor 是 Chat 应用层需要的模型执行器。
type Executor interface {
	Stream(context.Context, Request, TextSink) error
}

// TurnStore 是一次聊天生成需要的持久化写模型。
type TurnStore interface {
	BeginTurn(context.Context, BeginTurnInput) (BeginTurnResult, error)
	AppendAssistantText(context.Context, string, string) error
	FinishAssistant(context.Context, FinishAssistantInput) (conversation.Message, error)
	GetMessage(context.Context, string) (conversation.Message, error)
}

// Runtime 是 Chat 应用层对 Eino 可流式运行单元的最小依赖。
type Runtime interface {
	Stream(context.Context, RuntimeInput, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

// RuntimeBuilder 组装应用专属 Eino 运行时；提示词和 Graph 细节属于其实现。
type RuntimeBuilder interface {
	Build(context.Context, BuildInput) (Runtime, error)
}
