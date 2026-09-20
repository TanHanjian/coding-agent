package agentcontext

import (
	"context"
	"errors"

	"interview-memory-agent/backend/internal/domain/conversation"
)

var ErrHistorySelectorNotImplemented = errors.New("agent context: history selector is not implemented")

// HistorySelector chooses the history that can fit the budget plan. It returns
// both sides of the decision so summary orchestration can consume the omitted
// prefix without reimplementing selection rules.
type HistorySelector interface {
	Select(context.Context, HistorySelectionInput) (HistorySelection, error)
}

// HistorySelectionInput is the selector's complete decision input.
type HistorySelectionInput struct {
	History      []conversation.Message
	BudgetTokens int
}

// HistorySelection separates messages sent to the current model run from
// messages omitted by the window policy.
type HistorySelection struct {
	Included       []conversation.Message
	Omitted        []conversation.Message
	IncludedTokens int
	OmittedTokens  int
	Truncated      bool
}

// HistorySelectorFunc is an adapter for tests and incremental wiring.
type HistorySelectorFunc func(context.Context, HistorySelectionInput) (HistorySelection, error)

func (f HistorySelectorFunc) Select(ctx context.Context, input HistorySelectionInput) (HistorySelection, error) {
	return f(ctx, input)
}

// HistorySelectorDependencies are the lower-level seams needed by the
// default selector. The actual selection algorithm remains unimplemented.
type HistorySelectorDependencies struct {
	Model     string
	Estimator TokenEstimator
}

// DefaultHistorySelector is the concrete history-window selector skeleton.
type DefaultHistorySelector struct {
	model     string
	estimator TokenEstimator
}

func NewDefaultHistorySelector(deps HistorySelectorDependencies) (*DefaultHistorySelector, error) {
	if deps.Estimator == nil {
		return nil, errors.New("agent context: token estimator is required for history selector")
	}
	return &DefaultHistorySelector{
		model:     deps.Model,
		estimator: deps.Estimator,
	}, nil
}

func (*DefaultHistorySelector) Select(context.Context, HistorySelectionInput) (HistorySelection, error) {
	return HistorySelection{}, ErrHistorySelectorNotImplemented
}

var _ HistorySelector = (*DefaultHistorySelector)(nil)
