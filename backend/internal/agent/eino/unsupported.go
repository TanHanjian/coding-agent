package eino

import (
	"context"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// UnsupportedExecutor 让 HTTP 与服务装配在 Agent 尚未实现时仍然可运行。
// 用户实现 RuntimeBuilder 后，应改为注入 Executor，而不是修改该类型。
type UnsupportedExecutor struct{}

func NewUnsupportedExecutor() *UnsupportedExecutor { return &UnsupportedExecutor{} }

func (*UnsupportedExecutor) Stream(context.Context, chat.Request, chat.TextSink) error {
	return domainerr.ErrNotImplemented
}

var _ chat.Executor = (*UnsupportedExecutor)(nil)
