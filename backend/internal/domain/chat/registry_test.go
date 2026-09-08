package chat

import (
	"context"
	"testing"
)

func TestMemoryGenerationRegistryCancelsOnce(t *testing.T) {
	registry := NewMemoryGenerationRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	if !registry.Register("m1", cancel) || registry.Register("m1", cancel) {
		t.Fatal("expected exactly one active registration")
	}
	if !registry.Cancel("m1") || registry.Cancel("m1") {
		t.Fatal("expected exactly one successful cancellation")
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("expected cancelled context, got %v", ctx.Err())
	}
}
