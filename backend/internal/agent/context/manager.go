// Package agentcontext contains the concrete top-level context assembly module.
// Lower-level budget, history, summary, and token policies are intentionally
// left behind this module's seam and will be introduced incrementally.
package agentcontext

import (
	"context"
	"errors"
	"fmt"

	chat "interview-memory-agent/backend/internal/application/chat"
)

var ErrContextManagerOrchestrationNotImplemented = errors.New("agent context: manager orchestration is not implemented")

// Manager is the concrete top-level ContextManager. Its orchestration body is
// deliberately still a skeleton: the current fallback preserves the existing
// full-history behavior until the lower-level policies are implemented.
type Manager struct {
	fallback           chat.ContextManager
	budgetPolicy       BudgetPolicy
	tokenEstimator     TokenEstimator
	historySelector    HistorySelector
	summaryCoordinator SummaryCoordinator
	renderer           PreparedContextRenderer
}

type Option func(*Manager)

// WithBudgetPolicy reserves the budget-policy injection point.
func WithBudgetPolicy(policy BudgetPolicy) Option {
	return func(manager *Manager) {
		manager.budgetPolicy = policy
	}
}

// WithTokenEstimator reserves the tokenizer injection point.
func WithTokenEstimator(estimator TokenEstimator) Option {
	return func(manager *Manager) {
		manager.tokenEstimator = estimator
	}
}

// WithHistorySelector reserves the history-window injection point.
func WithHistorySelector(selector HistorySelector) Option {
	return func(manager *Manager) {
		manager.historySelector = selector
	}
}

// WithSummaryCoordinator reserves the summary orchestration injection point.
func WithSummaryCoordinator(coordinator SummaryCoordinator) Option {
	return func(manager *Manager) {
		manager.summaryCoordinator = coordinator
	}
}

// WithPreparedContextRenderer reserves the final prompt/runtime formatting
// injection point.
func WithPreparedContextRenderer(renderer PreparedContextRenderer) Option {
	return func(manager *Manager) {
		manager.renderer = renderer
	}
}

// NewManager constructs the top-level context manager. Future lower-level
// policies should be injected here instead of being created by callers.
func NewManager(options ...Option) *Manager {
	manager := &Manager{
		fallback: chat.PassThroughContextManager{},
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	return manager
}

// Prepare is the single entry point used by the Agent executor. When all
// orchestration seams are present it follows the top-down pipeline; otherwise
// it preserves the current pass-through behavior.
func (m *Manager) Prepare(ctx context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	if m == nil || m.fallback == nil {
		return chat.PreparedContext{}, errors.New("agent context: manager fallback is required")
	}
	if !m.pipelineReady() {
		return m.fallback.Prepare(ctx, input)
	}
	return m.prepareWithPolicies(ctx, input)
}

func (m *Manager) pipelineReady() bool {
	return m.budgetPolicy != nil &&
		m.historySelector != nil &&
		m.summaryCoordinator != nil &&
		m.renderer != nil
}

func (m *Manager) prepareWithPolicies(ctx context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	plan, err := m.budgetPolicy.Plan(ctx, input)
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("plan context budget: %w", err)
	}

	selection, err := m.historySelector.Select(ctx, HistorySelectionInput{
		History:      input.History,
		BudgetTokens: plan.HistoryTokens,
	})
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("select context history: %w", err)
	}

	summary, err := m.summaryCoordinator.Prepare(ctx, SummaryInput{
		ConversationID: input.Conversation.ID,
		OmittedHistory: selection.Omitted,
		BudgetTokens:   plan.SummaryTokens,
	})
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("prepare context summary: %w", err)
	}

	return m.renderer.Render(ctx, PreparedContextInput{
		Input:   input,
		Budget:  plan,
		History: selection,
		Summary: summary,
	})
}

var _ chat.ContextManager = (*Manager)(nil)
