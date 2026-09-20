package agentcontext

import (
	"context"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// SummaryCoordinator decides whether an omitted history prefix should be
// summarized or whether an existing summary can be reused.
type SummaryCoordinator interface {
	Prepare(context.Context, SummaryInput) (SummaryResult, error)
}

// SummaryInput is the complete input for one summary decision.
type SummaryInput struct {
	ConversationID  string
	OmittedHistory  []conversation.Message
	ExistingSummary *ContextSummaryRecord
	BudgetTokens    int
}

// ContextSummary is the versioned, structured content stored for a
// conversation. It contains no tool payloads, prompts, credentials, or hidden
// reasoning.
type ContextSummary struct {
	Version        int      `json:"version"`
	Goal           string   `json:"goal"`
	ConfirmedFacts []string `json:"confirmedFacts"`
	Decisions      []string `json:"decisions"`
	Constraints    []string `json:"constraints"`
	OpenQuestions  []string `json:"openQuestions"`
}

// ContextSummaryRecord is the persistence-neutral summary record.
type ContextSummaryRecord struct {
	ConversationID  string
	SchemaVersion   int
	CoveredSequence int
	Summary         ContextSummary
	UpdatedAt       time.Time
}

// SummaryResult reports the coordinator's decision without exposing summary
// source text in diagnostics.
type SummaryResult struct {
	Summary  *ContextSummaryRecord
	Used     bool
	Updated  bool
	Fallback bool
}

// SummaryCoordinatorFunc is an adapter for tests and incremental wiring.
type SummaryCoordinatorFunc func(context.Context, SummaryInput) (SummaryResult, error)

func (f SummaryCoordinatorFunc) Prepare(ctx context.Context, input SummaryInput) (SummaryResult, error) {
	return f(ctx, input)
}
