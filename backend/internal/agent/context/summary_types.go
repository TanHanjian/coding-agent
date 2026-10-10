package agentcontext

import (
	"context"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// SummaryCoordinator 协调摘要生成流程
type SummaryCoordinator interface {
	Prepare(ctx context.Context, input SummaryInput) (SummaryResult, error)
}

// ContextSummarizer 生成结构化摘要的接口
type ContextSummarizer interface {
	Summarize(ctx context.Context, input SummaryPrompt) (ContextSummary, error)
}

// SummaryPrompt 是摘要生成的输入 prompt
type SummaryPrompt struct {
	ConversationID  string
	ExistingSummary *ContextSummaryRecord
	History         []conversation.ConversationEntry // 待摘要的历史消息
	BudgetTokens    int
}

// ContextSummary 是结构化摘要内容（等同于 conversation.CompactionSummary）
type ContextSummary struct {
	Goal           string   `json:"goal"`
	ConfirmedFacts []string `json:"confirmedFacts"`
	Decisions      []string `json:"decisions"`
	Constraints    []string `json:"constraints"`
	OpenQuestions  []string `json:"openQuestions"`
}

// ContextSummaryRecord 是持久化的摘要记录
type ContextSummaryRecord struct {
	ConversationID string
	SchemaVersion  int
	Summary        ContextSummary
	UpdatedAt      time.Time
}

// SummaryInput 是 SummaryCoordinator.Prepare 的输入
type SummaryInput struct {
	ConversationID  string
	ExistingSummary *ContextSummaryRecord
	OmittedHistory  []conversation.ConversationEntry // 被省略的历史消息
	BudgetTokens    int
}

// SummaryResult 是 SummaryCoordinator.Prepare 的输出
type SummaryResult struct {
	Summary *ContextSummaryRecord
	Used    bool // 是否使用了摘要
	Updated bool // 是否生成了新摘要
}

// SummarizeInput 是 EinoContextSummarizer.Summarize 的输入
type SummarizeInput struct {
	PrefixMessages []conversation.ContextMessage
}

// SummarizeResult 是 EinoContextSummarizer.Summarize 的输出
type SummarizeResult struct {
	Summary conversation.CompactionSummary
}
