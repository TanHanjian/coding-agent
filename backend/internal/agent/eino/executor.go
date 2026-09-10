// Package eino 提供 Chat 应用层的 Eino Executor 适配器。
package eino

import (
	"context"
	"errors"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// Executor 是用户 Eino Runtime 的可编译占位实现。它不持有 SQLite 依赖，
// 也不决定 Graph、提示词、工具或模型。
type Executor struct {
	builder chat.RuntimeBuilder
}

func NewExecutor(builder chat.RuntimeBuilder) (*Executor, error) {
	if builder == nil {
		return nil, errors.New("chat executor: runtime builder is required")
	}
	return &Executor{builder: builder}, nil
}

// Stream 会在 Eino Graph 运行时完成后实现。
//
// 实现步骤：
//  1. 使用服务端可信的已持久化历史和新用户消息调用 builder.Build。
//  2. 将历史、当前用户消息和可信面试材料转换为 RuntimeInput，调用 runtime.Stream。
//  3. 消费 StreamReader，直至完成或 ctx 被取消。
//  4. 仅将用户可见的助手文本增量转发给 sink.WriteChunk。
//  5. 将预期的取消转换为 context.Canceled；包装其他错误时不得暴露服务商密钥。
//
// 此方法刻意返回 ErrNotImplemented，而不是发起模拟模型请求，避免未完成的
// Graph 行为持久化伪造输出。
func (e *Executor) Stream(context.Context, chat.Request, chat.TextSink) error {
	return domainerr.ErrNotImplemented
}

var _ chat.Executor = (*Executor)(nil)
