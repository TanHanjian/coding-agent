package agentcontext

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// TokenUsageObserver receives actual post-call usage reported by Eino. It is
// separate from TokenEstimator because usage is unavailable before a request.
type TokenUsageObserver interface {
	Observe(context.Context, string, *schema.TokenUsage)
}

// TokenUsageObserverFunc is an adapter for tests and incremental wiring.
type TokenUsageObserverFunc func(context.Context, string, *schema.TokenUsage)

func (f TokenUsageObserverFunc) Observe(ctx context.Context, model string, usage *schema.TokenUsage) {
	f(ctx, model, usage)
}
