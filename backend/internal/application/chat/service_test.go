package chat

import (
	"context"
	"sync"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

type cancelStoreStub struct {
	TurnStore

	mu       sync.Mutex
	message  conversation.Message
	getCalls int
}

func (s *cancelStoreStub) GetMessage(context.Context, string) (conversation.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	return s.message, nil
}

func (s *cancelStoreStub) setStatus(status conversation.MessageStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.message.Status = status
}

func (s *cancelStoreStub) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls
}

func TestSkeletonServiceCancelWaitsForGenerationCompletion(t *testing.T) {
	store := &cancelStoreStub{message: conversation.Message{
		ID:     "m1",
		Role:   conversation.MessageRoleAssistant,
		Status: conversation.MessageStatusStreaming,
	}}
	registry := NewMemoryGenerationRegistry()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	if !registry.Register("m1", runCancel) {
		t.Fatal("expected generation registration")
	}
	service := &SkeletonService{store: store, registry: registry}

	type result struct {
		message conversation.Message
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		message, err := service.Cancel(context.Background(), "m1")
		resultCh <- result{message: message, err: err}
	}()

	select {
	case <-runCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("expected Cancel to cancel the generation context")
	}
	if calls := store.calls(); calls != 1 {
		t.Fatalf("expected one initial message read before completion, got %d", calls)
	}
	select {
	case result := <-resultCh:
		t.Fatalf("Cancel returned before generation completion: %+v", result)
	default:
	}

	store.setStatus(conversation.MessageStatusCancelled)
	registry.Complete("m1")

	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("unexpected cancel error: %v", result.err)
		}
		if result.message.Status != conversation.MessageStatusCancelled {
			t.Fatalf("expected cancelled message, got %+v", result.message)
		}
	case <-time.After(time.Second):
		t.Fatal("expected Cancel to return after generation completion")
	}
}
