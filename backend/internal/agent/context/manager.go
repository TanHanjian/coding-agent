// Package agentcontext contains the concrete top-level context assembly module.
// Lower-level budget, history, summary, and token policies are intentionally
// left behind this module's seam and will be introduced incrementally.
package agentcontext

import (
	"context"
	"errors"
	"fmt"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

var ErrContextManagerOrchestrationNotImplemented = errors.New("agent context: manager orchestration is not implemented")

// Manager assembles persisted projections and retains the controlled legacy
// fallback. Budget and summary orchestration remain reserved for a later phase.
type Manager struct {
	fallback           chat.ContextManager
	projectionReader   conversation.ProjectionReader
	budgetPolicy       BudgetPolicy
	tokenEstimator     TokenEstimator
	historySelector    HistorySelector
	summaryCoordinator SummaryCoordinator
	renderer           PreparedContextRenderer
}

type Option func(*Manager)

// WithProjectionReader enables persisted, provenance-preserving history.
func WithProjectionReader(reader conversation.ProjectionReader) Option {
	return func(manager *Manager) { manager.projectionReader = reader }
}

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

// Prepare reads and converts persisted history before policy orchestration.
// Without a reader, only non-persisted requests can use the legacy fallback.
func (m *Manager) Prepare(ctx context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	if m == nil || m.fallback == nil {
		return chat.PreparedContext{}, errors.New("agent context: manager fallback is required")
	}
	if m.projectionReader != nil {
		if m.pipelineReady() {
			return chat.PreparedContext{}, errors.New("agent context: projection-aware policy pipeline is not implemented")
		}
		snapshot, err := m.projectionReader.ReadProjection(ctx, conversation.ProjectionQuery{
			ConversationID: input.Conversation.ID,
			RunID:          string(input.RunID),
			UserMessageID:  input.UserMessage.ID,
		})
		if err != nil {
			return chat.PreparedContext{}, fmt.Errorf("read context projection: %w", err)
		}
		history, err := ProjectSnapshot(snapshot)
		if err != nil {
			return chat.PreparedContext{}, fmt.Errorf("project context history: %w", err)
		}
		return chat.PreparedContext{
			ModelHistory:     conversation.CloneContextMessages(history),
			Query:            input.UserMessage.Content,
			InterviewContext: input.InterviewContext,
			Metadata:         chat.ContextMetadata{HistoryIncluded: len(history), ProjectionUsed: true, LegacyBeforeSequence: snapshot.LegacyBeforeSequence},
		}, nil
	}
	if input.RunID != "" {
		return chat.PreparedContext{}, errors.New("agent context: projection reader is required for a persisted run")
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

	// 转换 Omitted 为 ConversationEntry 数组（占位实现）
	// TODO: 需要完整的 Message -> ConversationEntry 转换逻辑
	historyEntries := make([]conversation.ConversationEntry, 0)

	summary, err := m.summaryCoordinator.Prepare(ctx, SummaryInput{
		ConversationID: input.Conversation.ID,
		OmittedHistory: historyEntries,
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
