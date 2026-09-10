package chat

import "context"

// generationRun 是一次后台生成的运行时对象，不持久化。它在 Start 成功创建
// Message 对后构造，并在 runGeneration 收尾后被释放。
type generationRun struct {
	Context context.Context
	Cancel  context.CancelFunc
	Request Request
	Sink    *bufferedTextSink
}
