// Package eino 提供 Chat 应用层的 Eino Executor 适配器。
package eino

import (
	"context"
	"errors"
	"fmt"
	"io"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// Executor 是用户 Eino Runtime 的应用层适配器。它不持有 SQLite 依赖，
// 也不决定 Graph、提示词、工具或模型。
type Executor struct {
	builder           chat.RuntimeBuilder
	graphDebugLogging bool
}

type Option func(*Executor)

// WithGraphDebugLogging enables per-run, metadata-only Graph lifecycle logs.
// It must be controlled by local configuration and is disabled by default.
func WithGraphDebugLogging(enabled bool) Option {
	return func(executor *Executor) {
		executor.graphDebugLogging = enabled
	}
}

func NewExecutor(builder chat.RuntimeBuilder, options ...Option) (*Executor, error) {
	if builder == nil {
		return nil, errors.New("chat executor: runtime builder is required")
	}
	executor := &Executor{builder: builder}
	for _, option := range options {
		if option != nil {
			option(executor)
		}
	}
	return executor, nil
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
	runtimeOptions := []compose.Option{compose.WithCallbacks(newGraphEventCallbacks(req, sink)...)}
	if e.graphDebugLogging {
		runtimeOptions = append(runtimeOptions, compose.WithCallbacks(newGraphDebugCallbacks(req)...))
	}
	reader, err := rt.Stream(ctx, chat.RuntimeInput{
		History:          schemaHistory,
		Query:            req.UserMessage.Content,
		InterviewContext: "",
	}, runtimeOptions...)
	if err != nil {
		return err
	}
	defer reader.Close()
	answerStarted := false
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

		if chunk == nil || chunk.Role == schema.Tool {
			continue
		}
		if len(chunk.ToolCalls) > 0 {
			if answerStarted {
				return errors.New("agent output protocol violation: tool call after final text")
			}
			continue
		}
		if chunk.Content == "" {
			continue
		}

		if !answerStarted {
			if err := sink.WriteEvent(ctx, chat.GenerationEvent{Kind: chat.GenerationEventPhase, Phase: "answering"}); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("write agent status: %w", err)
			}
			answerStarted = true
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
