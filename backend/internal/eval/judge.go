package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// LLMJudge asks a separately configured chat model for only rubric scores and
// a short rationale. The response is parsed strictly and retried once when it
// is not valid JSON.
type LLMJudge struct{ model model.BaseChatModel }

func NewLLMJudge(chatModel model.BaseChatModel) (*LLMJudge, error) {
	if chatModel == nil {
		return nil, errors.New("eval judge: model is required")
	}
	return &LLMJudge{model: chatModel}, nil
}

func (j *LLMJudge) Evaluate(ctx context.Context, input JudgeInput) (JudgeResult, error) {
	prompt := fmt.Sprintf(`你是一个严格的面试复盘 Agent 评测员。只根据给定问题、材料和回答评分，不补充事实。
请输出且只能输出 JSON，格式为：
{"scores":{"groundedness":0,"instructionFollowing":0,"completeness":0,"actionability":0,"rationale":"..."}}
四个分数都必须是 0 到 4 的数字。groundedness 评估是否忠于材料，instructionFollowing 评估是否遵守问题要求，completeness 评估是否覆盖关键事实，actionability 评估建议是否具体可执行。

问题：%s
面试材料：%s
工具调用摘要：%s
必须覆盖的事实：%s
额外评分要求：%s
最终回答：%s`, input.Query, input.InterviewContext, marshalForPrompt(input.ToolTrace), strings.Join(input.ExpectedFacts, "；"), input.Rubric, input.Answer)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		messages := []*schema.Message{schema.SystemMessage("你是可靠的 JSON 评分器。不要输出 Markdown。"), schema.UserMessage(prompt)}
		if attempt > 0 {
			messages = append(messages, schema.UserMessage("上一次输出不是合法 JSON。请严格只输出指定 JSON 对象。"))
		}
		message, err := j.model.Generate(ctx, messages)
		if err != nil {
			lastErr = err
			continue
		}
		if message != nil && message.ResponseMeta != nil {
			if capture := TraceCaptureFromContext(ctx); capture != nil {
				capture.AddUsage(message.ResponseMeta.Usage)
			}
		}
		var result JudgeResult
		if err := json.Unmarshal([]byte(stripCodeFence(message.Content)), &result); err != nil {
			lastErr = err
			continue
		}
		if err := validateJudge(result); err != nil {
			lastErr = err
			continue
		}
		return result, nil
	}
	return JudgeResult{}, fmt.Errorf("invalid judge response: %w", lastErr)
}

func validateJudge(result JudgeResult) error {
	values := []float64{result.Scores.Groundedness, result.Scores.InstructionFollowing, result.Scores.Completeness, result.Scores.Actionability}
	for _, value := range values {
		if value < 0 || value > 4 {
			return fmt.Errorf("judge score must be between 0 and 4")
		}
	}
	if strings.TrimSpace(result.Scores.Rationale) == "" {
		return errors.New("judge rationale is required")
	}
	return nil
}
func stripCodeFence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}
func marshalForPrompt(value any) string { data, _ := json.Marshal(value); return string(data) }
