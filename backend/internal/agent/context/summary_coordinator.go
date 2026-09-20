package agentcontext

import (
	"context"
	"errors"
)

var ErrSummaryCoordinatorNotImplemented = errors.New("agent context: summary coordinator is not implemented")

// SummaryCoordinatorDependencies are the lower-level seams required by the
// concrete summary coordinator.
type SummaryCoordinatorDependencies struct {
	Store      SummaryStore
	Summarizer ContextSummarizer
}

// Coordinator is the concrete SummaryCoordinator skeleton.
type Coordinator struct {
	store      SummaryStore
	summarizer ContextSummarizer
}

func NewSummaryCoordinator(deps SummaryCoordinatorDependencies) (*Coordinator, error) {
	if deps.Store == nil {
		return nil, errors.New("agent context: summary store is required")
	}
	if deps.Summarizer == nil {
		return nil, errors.New("agent context: context summarizer is required")
	}
	return &Coordinator{
		store:      deps.Store,
		summarizer: deps.Summarizer,
	}, nil
}

// Prepare is intentionally left as the implementation seam. It will later
// load an existing record, invoke the summarizer when needed, validate the
// result, and persist a monotonic CoveredSequence.
func (*Coordinator) Prepare(context.Context, SummaryInput) (SummaryResult, error) {
	return SummaryResult{}, ErrSummaryCoordinatorNotImplemented
}

var _ SummaryCoordinator = (*Coordinator)(nil)
