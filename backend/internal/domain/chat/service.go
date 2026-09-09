package chat

import (
	"context"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// Service 将协调已持久化的 Message、运行时注册表、执行器和 AI SDK 流编码器。
// HTTP 处理器必须依赖此接口，而非直接依赖 Eino。
type Service interface {
	Start(context.Context, StartInput) (StartResult, error)
	Subscribe(context.Context, string) (GenerationSubscription, error)
	Cancel(context.Context, string) (conversation.Message, error)
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

// Dependencies 有意采用端口式依赖。最终实现必须新增支持事务的会话操作，
// 以保证创建用户 Message 和流式助手 Message 与幂等性检查具有原子性。
type Dependencies struct {
	Executor    Executor
	Registry    GenerationRegistry
	Hub         GenerationHub
	RunContexts RunContextFactory
	// Store 是后续实现 Start/Cancel 时唯一允许写入聊天 Message 的端口。
	// 本阶段保持可选，以便 HTTP 骨架能返回明确的未实现响应。
	Store TurnStore
}

// SkeletonService 在用户实现 Eino 运行时和 UI Message Data Stream 编码器前，
// 保留应用层的组装扩展点。generation.go 把 Start 的运行对象、文本 sink 与
// 收尾职责显式拆开，避免 HTTP 连接进入 Eino 运行路径。
type SkeletonService struct {
	executor    Executor
	registry    GenerationRegistry
	hub         GenerationHub
	runContexts RunContextFactory
	store       TurnStore
}

func NewSkeletonService(deps Dependencies) (*SkeletonService, error) {
	if deps.Executor == nil || deps.Registry == nil || deps.Hub == nil || deps.RunContexts == nil {
		return nil, domainerr.ErrInvalidInput
	}
	return &SkeletonService{executor: deps.Executor, registry: deps.Registry, hub: deps.Hub, runContexts: deps.RunContexts, store: deps.Store}, nil
}

// Start 的实现步骤：
//  1. 校验仅含文本的 UIMessage 请求和客户端幂等键。
//  2. 原子地创建或复用用户 Message，并创建流式助手 Message。
//  3. 加载已持久化历史，并通过 runContexts.New 派生独立运行上下文；不得使用
//     HTTP 请求上下文作为 Eino 的取消父级。
//  4. 由 BeginTurnResult 组装 generationRun，并在 hub.Open 后注册助手 Message ID。
//  5. 在 goroutine 中调用 executor.Stream(run.Context, run.Request, run.Sink)。
//  6. goroutine 统一 flush sink、FinishAssistant、hub.Complete、registry.Complete。
func (s *SkeletonService) Start(context.Context, StartInput) (StartResult, error) {
	return StartResult{}, domainerr.ErrNotImplemented
}

// Subscribe 的实现步骤：
//  1. 读取 Store.GetMessage；非 streaming 消息返回只含最终 Snapshot 的订阅。
//  2. streaming 消息调用 Hub.Subscribe；Hub 原子返回 Snapshot 和 Updates。
//  3. HTTP 层先编码 Snapshot（替换全文），再编码 Updates（追加 delta/终态）。
func (s *SkeletonService) Subscribe(context.Context, string) (GenerationSubscription, error) {
	return GenerationSubscription{}, domainerr.ErrNotImplemented
}

// Cancel 的实现步骤：
//  1. 读取助手 Message；若已经取消，则原样返回。
//  2. 调用 Registry.Cancel，不能由 HTTP handler 直接 FinishAssistant。
//  3. generation goroutine 观察 ctx.Done 后统一 flush、FinishAssistant(cancelled)、
//     hub.Complete；这样取消和自然完成只有一个终态胜出。
//  4. Cancel 等待或重新读取最终 Message 后返回。
func (s *SkeletonService) Cancel(context.Context, string) (conversation.Message, error) {
	return conversation.Message{}, domainerr.ErrNotImplemented
}

var _ Service = (*SkeletonService)(nil)
