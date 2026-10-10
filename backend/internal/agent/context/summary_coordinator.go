package agentcontext

import (
	"context"
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// DefaultSummaryCoordinator 决策是否复用现有摘要或生成新摘要
type DefaultSummaryCoordinator struct {
	summarizer     ContextSummarizer
	tokenEstimator TokenEstimator
}

func NewDefaultSummaryCoordinator(summarizer ContextSummarizer, estimator TokenEstimator) *DefaultSummaryCoordinator {
	return &DefaultSummaryCoordinator{
		summarizer:     summarizer,
		tokenEstimator: estimator,
	}
}

func (c *DefaultSummaryCoordinator) Prepare(ctx context.Context, input SummaryInput) (SummaryResult, error) {
	// 检查是否可以复用现有摘要
	if input.ExistingSummary != nil {
		canReuse, err := c.canReuseSummary(input.ExistingSummary, input.OmittedHistory, input.BudgetTokens)
		if err != nil {
			return SummaryResult{}, fmt.Errorf("check reuse: %w", err)
		}
		if canReuse {
			return SummaryResult{
				Summary: input.ExistingSummary,
				Used:    true,
				Updated: false,
			}, nil
		}
	}

	// 需要生成新摘要
	summary, err := c.summarizer.Summarize(ctx, SummaryPrompt{
		ConversationID:  input.ConversationID,
		ExistingSummary: input.ExistingSummary,
		History:         input.OmittedHistory,
		BudgetTokens:    input.BudgetTokens,
	})
	if err != nil {
		return SummaryResult{}, fmt.Errorf("generate summary: %w", err)
	}

	// 验证摘要在预算内
	estimatedTokens := c.estimateSummaryTokens(summary)
	if estimatedTokens > input.BudgetTokens {
		return SummaryResult{}, fmt.Errorf("summary exceeds budget: %d > %d", estimatedTokens, input.BudgetTokens)
	}

	return SummaryResult{
		Summary: &ContextSummaryRecord{
			ConversationID: input.ConversationID,
			SchemaVersion:  1,
			Summary:        summary,
			UpdatedAt:      time.Now(),
		},
		Used:    true,
		Updated: true,
	}, nil
}

// canReuseSummary 检查现有摘要是否可以复用
func (c *DefaultSummaryCoordinator) canReuseSummary(existing *ContextSummaryRecord, newHistory []conversation.ConversationEntry, budget int) (bool, error) {
	// 如果没有新增历史，直接复用
	if len(newHistory) == 0 {
		return true, nil
	}

	// 检查摘要版本是否支持
	if existing.SchemaVersion != 1 {
		return false, nil
	}

	// 估算现有摘要的 token
	existingTokens := c.estimateSummaryTokens(existing.Summary)
	if existingTokens > budget {
		return false, nil
	}

	// 简单策略：如果新增历史较少（< 5 条消息），可以考虑复用
	// 实际实现中可以更复杂，例如检查新增内容是否影响摘要关键字段
	if len(newHistory) < 5 {
		return true, nil
	}

	return false, nil
}

// estimateSummaryTokens 估算摘要的 token 数
func (c *DefaultSummaryCoordinator) estimateSummaryTokens(summary ContextSummary) int {
	totalChars := len(summary.Goal)
	for _, fact := range summary.ConfirmedFacts {
		totalChars += len(fact)
	}
	for _, decision := range summary.Decisions {
		totalChars += len(decision)
	}
	for _, constraint := range summary.Constraints {
		totalChars += len(constraint)
	}
	for _, question := range summary.OpenQuestions {
		totalChars += len(question)
	}

	// 使用 TokenEstimator 的字符/token 比率
	// 简化实现：直接按 4 字符/token 估算
	return (totalChars + 3) / 4
}
