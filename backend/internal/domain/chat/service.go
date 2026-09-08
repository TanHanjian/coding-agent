package chat

import (
	"context"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// Service 将协调已持久化的 Message、运行时注册表、执行器和 AI SDK 流编码器。
// HTTP 处理器必须依赖此接口，而非直接依赖 Eino。
type Service interface {
	Start(context.Context, StartInput, TextSink) (StartResult, error)
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
	Executor Executor
	Registry GenerationRegistry
}

// SkeletonService 在用户实现 Eino 运行时和 UI Message Data Stream 编码器前，
// 保留应用层的组装扩展点。
type SkeletonService struct {
	executor Executor
	registry GenerationRegistry
}

func NewSkeletonService(deps Dependencies) (*SkeletonService, error) {
	if deps.Executor == nil || deps.Registry == nil {
		return nil, domainerr.ErrInvalidInput
	}
	return &SkeletonService{executor: deps.Executor, registry: deps.Registry}, nil
}

// Start 的实现步骤：
//  1. 校验仅含文本的 UIMessage 请求和客户端幂等键。
//  2. 原子地创建或复用用户 Message，并创建流式助手 Message。
//  3. 加载已持久化历史，并派生可取消的请求上下文。
//  4. 调用 executor.Stream 前注册助手 Message ID。
//  5. 将 sink 增量批量写入 SQLite 与 UI Message Data Stream。
//  6. 将助手 Message 置为 completed、cancelled 或 failed，并移除注册表条目。
func (s *SkeletonService) Start(context.Context, StartInput, TextSink) (StartResult, error) {
	return StartResult{}, domainerr.ErrNotImplemented
}

// Cancel 的实现步骤：
//  1. 读取助手 Message；若已经取消，则原样返回。
//  2. 从注册表获取其取消函数；completed 或 failed 的 Message 应返回冲突。
//  3. 等待生成清理逻辑刷写最后一批缓冲文本。
//  4. 返回已持久化的 cancelled Message。
func (s *SkeletonService) Cancel(context.Context, string) (conversation.Message, error) {
	return conversation.Message{}, domainerr.ErrNotImplemented
}

var _ Service = (*SkeletonService)(nil)
