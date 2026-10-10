package chat

import "errors"

// Model-call failures are application contracts. Agent implementations wrap
// these sentinels so finalization can classify failures without importing agents.
var (
	ErrContextBudgetExceeded = errors.New("model context budget exceeded")
	ErrContextCountingFailed = errors.New("model context could not be counted")
)

const (
	ContextBudgetExceededCode = "context_budget_exceeded"
	ContextCountingFailedCode = "context_counting_failed"
)

// SafeGenerationErrorText never exposes a provider error or model input.
func SafeGenerationErrorText(code string) string {
	switch code {
	case ContextBudgetExceededCode:
		return "本次请求的上下文超过模型容量，生成已停止。请缩小请求范围，或使用上下文容量更大的模型。"
	case ContextCountingFailedCode:
		return "本次请求的上下文无法安全计数，生成已停止。请检查模型和输入配置。"
	default:
		return "生成失败，请重新生成"
	}
}
