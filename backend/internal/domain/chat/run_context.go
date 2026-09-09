package chat

import (
	"context"
	"time"
)

// RunContextFactory 创建不从浏览器连接继承取消信号的生成上下文。用户刷新页面
// 只会断开 SSE 订阅；只有显式取消、超时或服务关闭才停止 Eino 和模型流。
type RunContextFactory interface {
	New() (context.Context, context.CancelFunc)
}

type DetachedRunContextFactory struct {
	root    context.Context
	timeout time.Duration
}

func NewDetachedRunContextFactory(root context.Context, timeout time.Duration) *DetachedRunContextFactory {
	if root == nil {
		root = context.Background()
	}
	return &DetachedRunContextFactory{root: root, timeout: timeout}
}

func (f *DetachedRunContextFactory) New() (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(f.root)
	if f.timeout <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, f.timeout)
}

var _ RunContextFactory = (*DetachedRunContextFactory)(nil)
