package agentcontext

import (
	"context"
	"errors"

	chat "interview-memory-agent/backend/internal/application/chat"
)

var ErrBudgetPolicyNotImplemented = errors.New("agent context: budget policy is not implemented")

// BudgetPolicy decides how much of each context section may be included in a
// model run. The policy owns model-window knowledge; Manager only orchestrates
// it and must not duplicate the arithmetic.
type BudgetPolicy interface {
	Plan(context.Context, chat.ContextInput) (BudgetPlan, error)
}

// BudgetPlan is the result of one budget-policy decision.
type BudgetPlan struct {
	ContextWindowTokens     int
	InstructionTokens       int
	SummaryTokens           int
	HistoryTokens           int
	InterviewMaterialTokens int
	ToolResultTokens        int
	OutputReservedTokens    int
}

// BudgetPolicyFunc is an adapter for tests and incremental wiring.
type BudgetPolicyFunc func(context.Context, chat.ContextInput) (BudgetPlan, error)

func (f BudgetPolicyFunc) Plan(ctx context.Context, input chat.ContextInput) (BudgetPlan, error) {
	return f(ctx, input)
}

// BudgetConfig contains configurable defaults for a model context window.
type BudgetConfig struct {
	ContextWindowTokens     int
	InstructionTokens       int
	SummaryTokens           int
	HistoryTokens           int
	InterviewMaterialTokens int
	ToolResultTokens        int
	OutputReservedTokens    int
}

func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		ContextWindowTokens:     32_000,
		InstructionTokens:       4_000,
		SummaryTokens:           2_000,
		HistoryTokens:           8_000,
		InterviewMaterialTokens: 6_000,
		ToolResultTokens:        8_000,
		OutputReservedTokens:    4_000,
	}
}

// DefaultBudgetPolicy is the configuration-backed policy skeleton.
type DefaultBudgetPolicy struct {
	config BudgetConfig
}

func NewDefaultBudgetPolicy(config BudgetConfig) *DefaultBudgetPolicy {
	return &DefaultBudgetPolicy{config: config}
}

func (p *DefaultBudgetPolicy) Plan(context.Context, chat.ContextInput) (BudgetPlan, error) {
	if p == nil {
		return BudgetPlan{}, errors.New("agent context: budget policy is nil")
	}
	return BudgetPlan{
		ContextWindowTokens:     p.config.ContextWindowTokens,
		InstructionTokens:       p.config.InstructionTokens,
		SummaryTokens:           p.config.SummaryTokens,
		HistoryTokens:           p.config.HistoryTokens,
		InterviewMaterialTokens: p.config.InterviewMaterialTokens,
		ToolResultTokens:        p.config.ToolResultTokens,
		OutputReservedTokens:    p.config.OutputReservedTokens,
	}, ErrBudgetPolicyNotImplemented
}

var _ BudgetPolicy = (*DefaultBudgetPolicy)(nil)
