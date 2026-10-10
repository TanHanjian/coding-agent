package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
)

// ModelCallConfig is the validated snapshot shared with the model client's
// output cap. ModelID identifies that configured client, not a window lookup.
type ModelCallConfig struct {
	ModelID             string
	ContextWindowTokens int
	OutputLimitTokens   int
	SafetyMarginTokens  int
}

func (c ModelCallConfig) Validate() error {
	if c.ModelID == "" || c.ContextWindowTokens <= 0 || c.OutputLimitTokens <= 0 || c.SafetyMarginTokens < 0 {
		return errors.New("model call budget configuration is invalid")
	}
	if c.OutputLimitTokens >= c.ContextWindowTokens || c.SafetyMarginTokens >= c.ContextWindowTokens-c.OutputLimitTokens {
		return errors.New("model output limit and safety margin leave no input capacity")
	}
	return nil
}

// ModelCallGuard has no run-specific state and is safe to share between Runs.
type ModelCallGuard struct {
	config    ModelCallConfig
	estimator TokenEstimator
}

func NewModelCallGuard(config ModelCallConfig, estimator TokenEstimator) (*ModelCallGuard, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if estimator == nil {
		return nil, errors.New("model call token estimator is required")
	}
	return &ModelCallGuard{config: config, estimator: estimator}, nil
}

func (g *ModelCallGuard) Check(ctx context.Context, phase string, call int, messages []*schema.Message, tools []*schema.ToolInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("%w: guard is required", chat.ErrContextCountingFailed)
	}
	started := time.Now()
	count, err := g.countMessages(messages, tools)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reason := "allowed"
	if err != nil || count < 0 {
		reason = "counting_failed"
	} else if count > g.config.ContextWindowTokens-g.config.OutputLimitTokens-g.config.SafetyMarginTokens {
		reason = "budget_exceeded"
	}
	// Normalize stage, do not log user-controlled identifiers or inputs.
	if phase != "chat" && phase != "summary" {
		phase = "unknown"
	}
	version := "custom"
	if _, ok := g.estimator.(*RequestTokenEstimator); ok {
		version = RequestEstimatorVersion
	}
	slog.DebugContext(ctx, "model call budget", "phase", phase, "call", call, "estimator", version, "input_estimate", count, "window", g.config.ContextWindowTokens, "output_limit", g.config.OutputLimitTokens, "safety_margin", g.config.SafetyMarginTokens, "reason", reason, "duration_ms", time.Since(started).Milliseconds())
	switch reason {
	case "counting_failed":
		return fmt.Errorf("%w: input representation is unsupported or invalid", chat.ErrContextCountingFailed)
	case "budget_exceeded":
		return fmt.Errorf("%w: input=%d available=%d", chat.ErrContextBudgetExceeded, count, g.config.ContextWindowTokens-g.config.OutputLimitTokens-g.config.SafetyMarginTokens)
	default:
		return nil
	}
}

// Recover only the counting boundary: malformed dependency schemas and custom
// estimator panics are rejected without exposing the panic value.
func (g *ModelCallGuard) countMessages(messages []*schema.Message, tools []*schema.ToolInfo) (count int, err error) {
	defer func() {
		if recover() != nil {
			count, err = 0, errors.New("request counting failed")
		}
	}()
	return g.estimator.CountMessages(g.config.ModelID, messages, tools)
}

// GuardedModelInvoker checks the completed summary prompt, with no chat tools.
func GuardedModelInvoker(guard *ModelCallGuard, invoke func(context.Context, []*schema.Message) (*schema.Message, error)) func(context.Context, []*schema.Message) (*schema.Message, error) {
	return func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if err := guard.Check(ctx, "summary", 1, messages, nil); err != nil {
			return nil, err
		}
		if invoke == nil {
			return nil, fmt.Errorf("%w: model invoker is required", chat.ErrContextCountingFailed)
		}
		return invoke(ctx, messages)
	}
}

// PreparationBudget distributes only available input capacity using the
// existing section ratios. The final guard still checks the actual request.
func PreparationBudget(config ModelCallConfig) (BudgetConfig, error) {
	if err := config.Validate(); err != nil {
		return BudgetConfig{}, err
	}
	available := config.ContextWindowTokens - config.OutputLimitTokens - config.SafetyMarginTokens
	defaults := DefaultBudgetConfig()
	weights := []int{defaults.InstructionTokens, defaults.SummaryTokens, defaults.HistoryTokens, defaults.InterviewMaterialTokens, defaults.ToolResultTokens}
	totalWeight := 0
	for _, weight := range weights {
		totalWeight += weight
	}
	sections := make([]int, len(weights))
	used := 0
	for i, weight := range weights {
		// Divide first to avoid available*weight overflowing an int.
		sections[i] = (available/totalWeight)*weight + ((available%totalWeight)*weight)/totalWeight
		used += sections[i]
	}
	sections[2] += available - used // rounding remainder goes to history
	return BudgetConfig{ContextWindowTokens: config.ContextWindowTokens, InstructionTokens: sections[0], SummaryTokens: sections[1], HistoryTokens: sections[2], InterviewMaterialTokens: sections[3], ToolResultTokens: sections[4], OutputReservedTokens: config.OutputLimitTokens}, nil
}
