// Package eino 提供 Chat 应用层的 Eino Executor 适配器。
package eino

import (
	"context"
	"errors"
	"fmt"
	"io"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"

	"github.com/cloudwego/eino/schema"
)

// Executor 是用户 Eino Runtime 的应用层适配器。它不持有 SQLite 依赖，
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

// Stream 的职责：
//  1. 使用服务端可信的已持久化历史和新用户消息调用 builder.Build。
//  2. 将历史和当前用户消息转换为 RuntimeInput，调用 runtime.Stream。
//  3. 消费 StreamReader，直至完成或 ctx 被取消。
//  4. 仅将用户可见的助手文本增量转发给 sink.WriteChunk。
//  5. 将取消返回为 ctx.Err，并包装其他运行或持久化错误。
func (e *Executor) Stream(ctx context.Context, req chat.Request, sink chat.TextSink) error {
	if sink == nil {
		return errors.New("chat executor: text sink is required")
	}

	rt, err := e.builder.Build(ctx, chat.BuildInput{
		Conversation: req.Conversation,
		History:      req.History,
		UserMessage:  req.UserMessage,
	})
	if err != nil {
		return err
	}
	schemaHistory, err := toSchemaHistory(req.History)
	if err != nil {
		return err
	}
	reader, err := rt.Stream(ctx, chat.RuntimeInput{
		History:          schemaHistory,
		Query:            req.UserMessage.Content,
		InterviewContext: "",
	})
	if err != nil {
		return err
	}
	defer reader.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("receive runtime stream: %w", err)
		}

		if chunk == nil {
			continue
		}
		if len(chunk.ToolCalls) > 0 {
			return errors.New("runtime stream emitted unsupported tool calls")
		}
		if chunk.Content == "" {
			continue
		}

		if err := sink.WriteChunk(ctx, chunk.Content); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("write assistant chunk: %w", err)
		}
	}
}

func toSchemaHistory(
	messages []conversation.Message,
) ([]*schema.Message, error) {
	history := make([]*schema.Message, 0, len(messages))

	for _, message := range messages {
		switch message.Role {
		case conversation.MessageRoleUser:
			history = append(history, schema.UserMessage(message.Content))

		case conversation.MessageRoleAssistant:
			history = append(
				history,
				schema.AssistantMessage(message.Content, nil),
			)

		default:
			return nil, fmt.Errorf("unsupported message role: %q", message.Role)
		}
	}

	return history, nil
}

var _ chat.Executor = (*Executor)(nil)
