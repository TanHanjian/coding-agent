package chat

import (
	"sync"

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
