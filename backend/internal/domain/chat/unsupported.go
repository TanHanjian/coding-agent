package chat

import (
	"context"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

// UnsupportedExecutor 让 HTTP 与服务装配在 Agent 尚未实现时仍然可运行。
// 用户实现 RuntimeBuilder 后，应改为注入 EinoExecutor，而不是修改该类型。
type UnsupportedExecutor struct{}

func NewUnsupportedExecutor() *UnsupportedExecutor { return &UnsupportedExecutor{} }

func (*UnsupportedExecutor) Stream(context.Context, Request, TextSink) error {
	return domainerr.ErrNotImplemented
}

var _ Executor = (*UnsupportedExecutor)(nil)
