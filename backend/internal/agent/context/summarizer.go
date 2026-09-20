package agentcontext

import (
	"context"
	"errors"

	"interview-memory-agent/backend/internal/domain/conversation"
)

var ErrContextSummarizerNotImplemented = errors.New("agent context: context summarizer is not implemented")

// ContextSummarizer turns an omitted history prefix into a validated,
// persistence-neutral summary. The implementation may use the current
// ChatModel, but this seam hides Eino and model configuration.
type ContextSummarizer interface {
	Summarize(context.Context, SummaryPrompt) (ContextSummary, error)
}

// SummaryPrompt is the structured input for one summarization attempt.
type SummaryPrompt struct {
	ConversationID  string
	ExistingSummary *ContextSummaryRecord
	History         []conversation.Message
	BudgetTokens    int
}

// ContextSummarizerFunc is an adapter for tests and incremental wiring.
type ContextSummarizerFunc func(context.Context, SummaryPrompt) (ContextSummary, error)

func (f ContextSummarizerFunc) Summarize(ctx context.Context, input SummaryPrompt) (ContextSummary, error) {
	return f(ctx, input)
}
