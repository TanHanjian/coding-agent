package chat

import (
	"context"
	"errors"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

// EinoExecutor 是用户 Agent 运行时的可编译占位实现。它不持有 SQLite 依赖，
// 也不决定提示词、工具或模型。
type EinoExecutor struct {
	builder AgentBuilder
}

func NewEinoExecutor(builder AgentBuilder) (*EinoExecutor, error) {
	if builder == nil {
		return nil, errors.New("chat executor: agent builder is required")
	}
	return &EinoExecutor{builder: builder}, nil
}

// Stream 会在 Eino Agent 主体完成后实现。
//
// 实现步骤：
//  1. 使用服务端可信的已持久化历史和新用户消息调用 builder.Build。
//  2. 为本次请求范围内的 Agent 创建 Eino Runner。
//  3. 消费 Runner 事件，直至完成或 ctx 被取消。
//  4. 仅将用户可见的文本增量转发给 sink.AppendText。
//  5. 将预期的取消转换为 context.Canceled；包装其他错误时不得暴露服务商密钥。
//
// 此方法刻意返回 ErrNotImplemented，而不是发起模拟模型请求，避免未完成的
// Agent 行为持久化伪造输出。
func (e *EinoExecutor) Stream(context.Context, Request, TextSink) error {
	return domainerr.ErrNotImplemented
}

var _ Executor = (*EinoExecutor)(nil)
