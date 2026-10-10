package chat

import (
	"context"
	"sync"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// OwnedGenerationHub provides a lease for one new execution. A failed claim
// must leave every existing snapshot and subscriber untouched.
type OwnedGenerationHub interface {
	GenerationHub
	Claim(conversation.Message) (GenerationHubLease, bool)
}

type GenerationHubLease interface {
	Complete(conversation.Message)
	Release()
}

type OwnedGenerationRegistry interface {
	GenerationRegistry
	Claim(string, context.CancelFunc) (GenerationRegistryLease, bool)
}

type GenerationRegistryLease interface{ Release() }

type memoryHubLease struct {
	hub  *MemoryGenerationHub
	id   string
	once sync.Once
}

func (h *MemoryGenerationHub) Claim(message conversation.Message) (GenerationHubLease, bool) {
	if message.ID == "" || message.Role != conversation.MessageRoleAssistant || message.Status != conversation.MessageStatusStreaming {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs[message.ID] != nil {
		return nil, false
	}
	lease := &memoryHubLease{hub: h, id: message.ID}
	h.runs[message.ID] = &memoryGeneration{owner: lease, content: message.Content, status: message.Status, subscribers: make(map[chan GenerationUpdate]struct{})}
	return lease, true
}

func (l *memoryHubLease) Complete(message conversation.Message) {
	if message.ID != l.id || message.Role != conversation.MessageRoleAssistant || message.Status == conversation.MessageStatusStreaming {
		return
	}
	l.once.Do(func() {
		l.hub.mu.Lock()
		defer l.hub.mu.Unlock()
		run := l.hub.runs[l.id]
		if run == nil || run.owner != l {
			return
		}
		run.content, run.status = message.Content, message.Status
		l.hub.broadcast(run, GenerationUpdate{Kind: GenerationUpdateTerminal, Status: message.Status})
		for ch := range run.subscribers {
			close(ch)
		}
		delete(l.hub.runs, l.id)
	})
}

// Release closes an owned subscription without inventing a persisted terminal.
func (l *memoryHubLease) Release() {
	l.once.Do(func() {
		l.hub.mu.Lock()
		defer l.hub.mu.Unlock()
		run := l.hub.runs[l.id]
		if run == nil || run.owner != l {
			return
		}
		for ch := range run.subscribers {
			close(ch)
		}
		delete(l.hub.runs, l.id)
	})
}

type memoryRegistryLease struct {
	registry *MemoryGenerationRegistry
	id       string
	once     sync.Once
}

func (r *MemoryGenerationRegistry) Claim(id string, cancel context.CancelFunc) (GenerationRegistryLease, bool) {
	if id == "" || cancel == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs[id] != nil {
		return nil, false
	}
	lease := &memoryRegistryLease{registry: r, id: id}
	r.runs[id] = &generationControl{owner: lease, cancel: cancel, done: make(chan struct{})}
	return lease, true
}

func (l *memoryRegistryLease) Release() {
	l.once.Do(func() {
		l.registry.mu.Lock()
		defer l.registry.mu.Unlock()
		run := l.registry.runs[l.id]
		if run == nil || run.owner != l {
			return
		}
		delete(l.registry.runs, l.id)
		close(run.done)
	})
}

var _ OwnedGenerationHub = (*MemoryGenerationHub)(nil)
var _ OwnedGenerationRegistry = (*MemoryGenerationRegistry)(nil)
