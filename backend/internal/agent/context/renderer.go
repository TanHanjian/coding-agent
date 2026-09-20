package agentcontext

import (
	"context"

	chat "interview-memory-agent/backend/internal/application/chat"
)

// PreparedContextRenderer converts lower-level policy outputs into the
// runtime-neutral PreparedContext consumed by the executor.
type PreparedContextRenderer interface {
	Render(context.Context, PreparedContextInput) (chat.PreparedContext, error)
}

// PreparedContextInput is the complete input available to the final renderer.
type PreparedContextInput struct {
	Input   chat.ContextInput
	Budget  BudgetPlan
	History HistorySelection
	Summary SummaryResult
}

// PreparedContextRendererFunc is an adapter for tests and incremental wiring.
type PreparedContextRendererFunc func(context.Context, PreparedContextInput) (chat.PreparedContext, error)

func (f PreparedContextRendererFunc) Render(ctx context.Context, input PreparedContextInput) (chat.PreparedContext, error) {
	return f(ctx, input)
}
