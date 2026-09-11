package chat

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// Service 将协调已持久化的 Message、运行时注册表、执行器和 AI SDK 流编码器。
// HTTP 处理器必须依赖此接口，而非直接依赖 Eino。
type Service interface {
	Start(ctx context.Context, input StartInput) (StartResult, error)
	Subscribe(ctx context.Context, assistantMessageID string) (GenerationSubscription, error)
	Cancel(ctx context.Context, assistantMessageID string) (conversation.Message, error)
}

type StartInput struct {
	ConversationID  string
	ClientMessageID string
	Text            string
}

type StartResult struct {
	ConversationID     string
	UserMessageID      string
	AssistantMessageID string
}

// Dependencies 是 Chat 应用层协调一次生成所需的外部依赖。
type Dependencies struct {
	Executor    Executor
	Registry    GenerationRegistry
	Hub         GenerationHub
	RunContexts RunContextFactory
	// Store 是 Chat Message 的唯一写入端口。
	Store TurnStore
}

// SkeletonService 保留 Eino 运行时和流编码器尚未完成时的应用层组装入口。
// run.go、writer.go 和 buffer.go 将生成生命周期、文本提交与缓存策略分开。
type SkeletonService struct {
	executor    Executor
	registry    GenerationRegistry
	hub         GenerationHub
	runContexts RunContextFactory
	store       TurnStore
}

func NewSkeletonService(deps Dependencies) (*SkeletonService, error) {
	if deps.Executor == nil || deps.Registry == nil || deps.Hub == nil || deps.RunContexts == nil || deps.Store == nil {
		return nil, domainerr.ErrInvalidInput
	}
	return &SkeletonService{executor: deps.Executor, registry: deps.Registry, hub: deps.Hub, runContexts: deps.RunContexts, store: deps.Store}, nil
}

func (s *SkeletonService) runGeneration(run generationRun) {
	assistantMessageId := run.Request.AssistantMessage.ID
	defer run.Cancel()
	defer s.registry.Complete(assistantMessageId)
	streamErr := s.executor.Stream(run.Context, run.Request, run.Sink)
	finalizeCtx, cancel := context.WithTimeout(
		context.WithoutCancel(run.Context),
		5*time.Second,
	)
	defer cancel()

	if flushErr := run.Sink.Flush(finalizeCtx); streamErr == nil && flushErr != nil {
		streamErr = flushErr
	}

	status := conversation.MessageStatusCompleted
	if errors.Is(streamErr, context.Canceled) {
		status = conversation.MessageStatusCancelled
	} else if streamErr != nil {
		status = conversation.MessageStatusFailed
		slog.Error("chat generation failed",
			"conversation_id", run.Request.Conversation.ID,
			"assistant_message_id", assistantMessageId,
			"error", streamErr,
		)
	}

	finalMessage, err := s.store.FinishAssistant(finalizeCtx, FinishAssistantInput{
		AssistantMessageID: assistantMessageId,
		Status:             status,
	})
	if err != nil {
		slog.Error("persist chat terminal message failed",
			"conversation_id", run.Request.Conversation.ID,
			"assistant_message_id", assistantMessageId,
			"status", status,
			"error", err,
		)
		// 不能把未成功持久化的终态广播给前端。
		return
	}

	s.hub.Complete(finalMessage)
}

// Start 的实现步骤：
//  1. 校验仅含文本的 UIMessage 请求和客户端幂等键。
//  2. 原子地创建或复用用户 Message，并创建流式助手 Message。
//  3. 加载已持久化历史，并通过 runContexts.New 派生独立运行上下文；不得使用
//     HTTP 请求上下文作为 Eino 的取消父级。
//  4. 由 BeginTurnResult 组装 generationRun，并在 hub.Open 后注册助手 Message ID。
//  5. 在 goroutine 中调用 executor.Stream(run.Context, run.Request, run.Sink)。
//  6. goroutine 统一 flush sink、FinishAssistant、hub.Complete、registry.Complete。
func (s *SkeletonService) Start(ctx context.Context, input StartInput) (StartResult, error) {
	// 1. 基础校验
	if strings.TrimSpace(input.ConversationID) == "" ||
		strings.TrimSpace(input.ClientMessageID) == "" ||
		strings.TrimSpace(input.Text) == "" {
		return StartResult{}, domainerr.ErrInvalidInput
	}

	turn, err := s.store.BeginTurn(ctx, BeginTurnInput(input))
	if err != nil {
		return StartResult{}, err
	}
	result := StartResult{
		ConversationID:     turn.Conversation.ID,
		UserMessageID:      turn.UserMessage.ID,
		AssistantMessageID: turn.AssistantMessage.ID,
	}
	if turn.Reused {
		return result, nil
	}
	runctx, cancel := s.runContexts.New()
	writer := newGenerationTextWriter(
		s.store,
		s.hub,
		turn.AssistantMessage.ID,
	)
	sink := newBufferedTextSink(
		writer,
		turn.AssistantMessage.ID,
		DefaultTextBufferPolicy,
	)
	run := generationRun{
		Context: runctx,
		Cancel:  cancel,
		Request: Request{
			Conversation:     turn.Conversation,
			UserMessage:      turn.UserMessage,
			AssistantMessage: turn.AssistantMessage,
			History:          turn.History,
		},
		Sink: sink,
	}
	s.hub.Open(turn.AssistantMessage)
	if !s.registry.Register(turn.AssistantMessage.ID, cancel) {
		cancel()
		return StartResult{}, domainerr.ErrConflict
	}

	// 5. 后台执行；Start 到这里立即返回给 HTTP。
	go s.runGeneration(run)

	return result, nil
}

// Subscribe 的实现步骤：
//  1. 读取 Store.GetMessage；非 streaming 消息返回只含最终 Snapshot 的订阅。
//  2. streaming 消息调用 Hub.Subscribe；Hub 原子返回 Snapshot 和 Updates。
//  3. HTTP 层先编码 Snapshot（替换全文），再编码 Updates（追加 delta/终态）。
func (s *SkeletonService) Subscribe(ctx context.Context, assistantMessageID string) (GenerationSubscription, error) {
	message, err := s.store.GetMessage(ctx, assistantMessageID)
	if err != nil {
		return GenerationSubscription{}, err
	}
	if message.Status != "streaming" {
		return GenerationSubscription{
			Snapshot: GenerationSnapshot{
				Content:            message.Content,
				AssistantMessageID: message.ID,
				Status:             message.Status,
			},
			Updates: nil,
			close:   func() {},
		}, nil
	}
	subscription, ok := s.hub.Subscribe(assistantMessageID)
	if ok {
		return subscription, nil
	}

	// Hub 只保存当前进程内仍在运行的生成。若运行已结束或服务重启导致
	// 内存状态丢失，返回 Store 中的最后检查点，调用方不应继续等待 Updates。
	return GenerationSubscription{
		Snapshot: GenerationSnapshot{
			Content:            message.Content,
			AssistantMessageID: message.ID,
			Status:             message.Status,
		},
		Updates: nil,
		close:   func() {},
	}, nil
}

// Cancel 的实现步骤：
//  1. 读取助手 Message；若已经取消，则原样返回。
//  2. 调用 Registry.Cancel，不能由 HTTP handler 直接 FinishAssistant。
//  3. generation goroutine 观察 ctx.Done 后统一 flush、FinishAssistant(cancelled)、
//     hub.Complete；这样取消和自然完成只有一个终态胜出。
//  4. Cancel 等待或重新读取最终 Message 后返回。
func (s *SkeletonService) Cancel(ctx context.Context, assistantMessageID string) (conversation.Message, error) {
	message, err := s.store.GetMessage(ctx, assistantMessageID)
	if err != nil {
		return message, err
	}
	if message.Status != conversation.MessageStatusStreaming {
		return message, nil
	}
	done, active := s.registry.Cancel(assistantMessageID)
	if !active {
		// 生成可能恰好已完成，或服务重启后已丢失运行时取消句柄。
		return s.store.GetMessage(ctx, assistantMessageID)
	}

	select {
	case <-done:
		return s.store.GetMessage(ctx, assistantMessageID)
	case <-ctx.Done():
		return conversation.Message{}, ctx.Err()
	}
}

var _ Service = (*SkeletonService)(nil)
