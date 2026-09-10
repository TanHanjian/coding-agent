package chat

import (
	"context"
	"sync"
)

// GenerationRegistry 保存请求范围内的取消函数。首版 Eino 执行不支持恢复，
// 因此它有意仅存在于内存中。
type GenerationRegistry interface {
	Register(assistantMessageID string, cancel context.CancelFunc) bool
	Cancel(assistantMessageID string) bool
	Complete(assistantMessageID string)
}

// MemoryGenerationRegistry 可安全地被并发的聊天和取消处理器使用。
type MemoryGenerationRegistry struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewMemoryGenerationRegistry() *MemoryGenerationRegistry {
	return &MemoryGenerationRegistry{cancels: make(map[string]context.CancelFunc)}
}

// Register 占用一个助手 Message ID。返回 false 表示已有活跃生成占用该 ID，
// 不得替换。
func (r *MemoryGenerationRegistry) Register(assistantMessageID string, cancel context.CancelFunc) bool {
	if assistantMessageID == "" || cancel == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.cancels[assistantMessageID]; exists {
		return false
	}
	r.cancels[assistantMessageID] = cancel
	return true
}

// Cancel 获取并仅调用一次取消函数。完成与取消之间的竞争由同一把互斥锁串行化。
func (r *MemoryGenerationRegistry) Cancel(assistantMessageID string) bool {
	r.mu.Lock()
	cancel, exists := r.cancels[assistantMessageID]
	if exists {
		delete(r.cancels, assistantMessageID)
	}
	r.mu.Unlock()
	if exists {
		cancel()
	}
	return exists
}

// Complete 在进入终态后移除仅供运行时使用的取消句柄。
func (r *MemoryGenerationRegistry) Complete(assistantMessageID string) {
	r.mu.Lock()
	delete(r.cancels, assistantMessageID)
	r.mu.Unlock()
}

var _ GenerationRegistry = (*MemoryGenerationRegistry)(nil)
