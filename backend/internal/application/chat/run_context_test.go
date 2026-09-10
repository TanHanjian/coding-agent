package chat

import (
	"context"
	"testing"
	"time"
)

func TestDetachedRunContextIgnoresSourceCancellation(t *testing.T) {
	source, stopSource := context.WithCancel(context.Background())
	factory := NewDetachedRunContextFactory(source, time.Minute)
	runContext, stopRun := factory.New()
	stopSource()
	if runContext.Err() != nil {
		t.Fatalf("request cancellation must not cancel generation: %v", runContext.Err())
	}
	stopRun()
	if runContext.Err() != context.Canceled {
		t.Fatalf("explicit generation cancellation must win, got %v", runContext.Err())
	}
}
