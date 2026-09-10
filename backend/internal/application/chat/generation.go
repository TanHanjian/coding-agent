package chat

import (
	"context"
	"sync"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// MemoryGenerationHub 是首版的单进程 snapshot + delta 广播器。它只服务仍在
// 运行的生成；服务重启后无法恢复上游模型流，前端应显示已持久化的最后内容。
type MemoryGenerationHub struct {
	mu   sync.Mutex
	runs map[string]*memoryGeneration
}

type memoryGeneration struct {
	content     string
	status      conversation.MessageStatus
	subscribers map[chan GenerationUpdate]struct{}
}

func NewMemoryGenerationHub() *MemoryGenerationHub {
	return &MemoryGenerationHub{runs: make(map[string]*memoryGeneration)}
}

func (h *MemoryGenerationHub) Open(message conversation.Message) {
	if message.ID == "" || message.Role != conversation.MessageRoleAssistant || message.Status != conversation.MessageStatusStreaming {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.runs[message.ID]; !exists {
		h.runs[message.ID] = &memoryGeneration{content: message.Content, status: message.Status, subscribers: make(map[chan GenerationUpdate]struct{})}
	}
}

func (h *MemoryGenerationHub) PublishText(assistantMessageID, text string) {
	if assistantMessageID == "" || text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run := h.runs[assistantMessageID]
	if run == nil || run.status != conversation.MessageStatusStreaming {
		return
	}
	run.content += text
	h.broadcast(run, GenerationUpdate{Kind: GenerationUpdateDelta, Text: text})
}

func (h *MemoryGenerationHub) Complete(message conversation.Message) {
	if message.ID == "" || message.Role != conversation.MessageRoleAssistant || message.Status == conversation.MessageStatusStreaming {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	run := h.runs[message.ID]
	if run == nil {
		return
	}
	run.content = message.Content
	run.status = message.Status
	h.broadcast(run, GenerationUpdate{Kind: GenerationUpdateTerminal, Status: message.Status})
	for subscriber := range run.subscribers {
		close(subscriber)
	}
	delete(h.runs, message.ID)
}

func (h *MemoryGenerationHub) Subscribe(assistantMessageID string) (GenerationSubscription, bool) {
	ch := make(chan GenerationUpdate, 32)
	var once sync.Once
	h.mu.Lock()
	run := h.runs[assistantMessageID]
	if run == nil {
		h.mu.Unlock()
		return GenerationSubscription{}, false
	}
	run.subscribers[ch] = struct{}{}
	snapshot := GenerationSnapshot{AssistantMessageID: assistantMessageID, Content: run.content, Status: run.status}
	h.mu.Unlock()
	return GenerationSubscription{Snapshot: snapshot, Updates: ch, close: func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if current := h.runs[assistantMessageID]; current != nil {
				delete(current.subscribers, ch)
			}
		})
	}}, true
}

func (h *MemoryGenerationHub) broadcast(run *memoryGeneration, update GenerationUpdate) {
	for subscriber := range run.subscribers {
		select {
		case subscriber <- update:
		default:
			// 慢客户端刷新后可重新订阅并得到完整 Snapshot，不能阻塞模型输出。
		}
	}
}

var _ GenerationHub = (*MemoryGenerationHub)(nil)

// GenerationSnapshot 是订阅建立瞬间的完整可见状态。
type GenerationSnapshot struct {
	AssistantMessageID string
	Content            string
	Status             conversation.MessageStatus
}

type GenerationUpdate struct {
	Kind   GenerationUpdateKind
	Text   string
	Status conversation.MessageStatus
}

type GenerationUpdateKind string

const (
	GenerationUpdateDelta    GenerationUpdateKind = "delta"
	GenerationUpdateTerminal GenerationUpdateKind = "terminal"
)

// GenerationHub 是单进程内仍在运行的生成任务及其订阅者集合。
type GenerationHub interface {
	Open(conversation.Message)
	PublishText(assistantMessageID, text string)
	Complete(conversation.Message)
	Subscribe(assistantMessageID string) (GenerationSubscription, bool)
}

type GenerationSubscription struct {
	Snapshot GenerationSnapshot
	Updates  <-chan GenerationUpdate
	close    func()
}

func (s GenerationSubscription) Close() {
	if s.close != nil {
		s.close()
	}
}

// GenerationRegistry 保存请求范围内的取消函数。首版 Eino 执行不支持恢复，
// 因此它有意仅存在于内存中。
type GenerationRegistry interface {
	Register(assistantMessageID string, cancel context.CancelFunc) bool
	Cancel(assistantMessageID string) (done <-chan struct{}, active bool)
	Complete(assistantMessageID string)
}

// MemoryGenerationRegistry 可安全地被并发的聊天和取消处理器使用。
type MemoryGenerationRegistry struct {
	mu   sync.Mutex
	runs map[string]*generationControl
}

type generationControl struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewMemoryGenerationRegistry() *MemoryGenerationRegistry {
	return &MemoryGenerationRegistry{runs: make(map[string]*generationControl)}
}

// Register 占用一个助手 Message ID。返回 false 表示已有活跃生成占用该 ID，
// 不得替换。
func (r *MemoryGenerationRegistry) Register(assistantMessageID string, cancel context.CancelFunc) bool {
	if assistantMessageID == "" || cancel == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.runs[assistantMessageID]; exists {
		return false
	}
	r.runs[assistantMessageID] = &generationControl{cancel: cancel, done: make(chan struct{})}
	return true
}

// Cancel 获取并仅调用一次取消函数，并返回在生成收尾完成后关闭的 done channel。
// 多个取消请求可等待同一 done channel；完成与取消之间的竞争由同一把互斥锁串行化。
func (r *MemoryGenerationRegistry) Cancel(assistantMessageID string) (<-chan struct{}, bool) {
	r.mu.Lock()
	run, exists := r.runs[assistantMessageID]
	if !exists {
		r.mu.Unlock()
		return nil, false
	}
	done := run.done
	cancel := run.cancel
	run.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return done, true
}

// Complete 在终态持久化和广播完成后移除运行控制对象，并唤醒等待取消结果的调用方。
func (r *MemoryGenerationRegistry) Complete(assistantMessageID string) {
	r.mu.Lock()
	run := r.runs[assistantMessageID]
	if run != nil {
		delete(r.runs, assistantMessageID)
		close(run.done)
	}
	r.mu.Unlock()
}

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

// generationRun 是一次后台生成的运行时对象，不持久化。它在 Start 成功创建
// Message 对后构造，并在 runGeneration 收尾后被释放。
type generationRun struct {
	Context context.Context
	Cancel  context.CancelFunc
	Request Request
	Sink    *bufferedTextSink
}

var _ GenerationHub = (*MemoryGenerationHub)(nil)
var _ GenerationRegistry = (*MemoryGenerationRegistry)(nil)
var _ RunContextFactory = (*DetachedRunContextFactory)(nil)
