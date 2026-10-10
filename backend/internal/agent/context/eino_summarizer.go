package agentcontext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

var ErrSummarizationFailed = errors.New("agent context: summarization failed")

// EinoContextSummarizer 使用 Eino ChatModel 生成结构化摘要
type EinoContextSummarizer struct {
	modelInvoker func(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
}

// NewEinoContextSummarizer 创建基于 Eino 的摘要生成器
func NewEinoContextSummarizer(invoker func(ctx context.Context, messages []*schema.Message) (*schema.Message, error)) *EinoContextSummarizer {
	return &EinoContextSummarizer{modelInvoker: invoker}
}

func (s *EinoContextSummarizer) Summarize(ctx context.Context, input SummaryPrompt) (ContextSummary, error) {
	if s == nil || s.modelInvoker == nil {
		return ContextSummary{}, errors.New("agent context: summarizer or model invoker is nil")
	}

	// 构造摘要 prompt
	systemPrompt := `You are a conversation summarizer. Given a conversation history prefix, extract:
1. goal: the user's primary objective or question
2. confirmedFacts: key facts established in the conversation (array of strings)
3. decisions: important decisions or agreements made (array of strings)
4. constraints: limitations or requirements (array of strings)
5. openQuestions: unresolved questions (array of strings)

Output valid JSON with these five fields.`

	// 将 History entries 转换为文本
	var historyText string
	for _, entry := range input.History {
		if entry.Message != nil {
			// 拼接所有 content parts
			var contentText string
			for _, part := range entry.Message.Content {
				if part.Text != nil {
					contentText += *part.Text
				}
			}
			historyText += fmt.Sprintf("[%s]: %s\n", entry.Message.Role, contentText)
		}
	}

	// 如果有现有摘要，提示模型更新而非重写
	var userPrompt string
	if input.ExistingSummary != nil {
		existingJSON, _ := json.Marshal(input.ExistingSummary.Summary)
		userPrompt = fmt.Sprintf("Update this existing summary with new conversation:\n\nExisting summary:\n%s\n\nNew conversation:\n%s", string(existingJSON), historyText)
	} else {
		userPrompt = fmt.Sprintf("Summarize this conversation prefix:\n\n%s", historyText)
	}

	messages := []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{Role: schema.User, Content: userPrompt},
	}

	// 调用模型
	response, err := s.modelInvoker(ctx, messages)
	if err != nil {
		return ContextSummary{}, fmt.Errorf("model invocation failed: %w", err)
	}

	if response == nil || response.Content == "" {
		return ContextSummary{}, fmt.Errorf("%w: empty response", ErrSummarizationFailed)
	}

	// 解析 JSON
	var summary ContextSummary
	if err := json.Unmarshal([]byte(response.Content), &summary); err != nil {
		return ContextSummary{}, fmt.Errorf("%w: invalid JSON: %v", ErrSummarizationFailed, err)
	}

	// 验证必填字段
	if summary.Goal == "" {
		return ContextSummary{}, fmt.Errorf("%w: missing goal", ErrSummarizationFailed)
	}

	return summary, nil
}
