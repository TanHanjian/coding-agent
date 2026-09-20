package agentcontext

import "github.com/cloudwego/eino/schema"

// TokenEstimator abstracts model-specific token counting before a model call.
// Implementations may use an exact tokenizer or a conservative fallback.
type TokenEstimator interface {
	CountText(model string, text string) (int, error)
	CountMessages(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error)
}

// TokenEstimatorFunc is an adapter for tests and incremental wiring.
type TokenEstimatorFunc struct {
	CountTextFunc     func(model string, text string) (int, error)
	CountMessagesFunc func(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error)
}

func (f TokenEstimatorFunc) CountText(model string, text string) (int, error) {
	return f.CountTextFunc(model, text)
}

func (f TokenEstimatorFunc) CountMessages(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error) {
	return f.CountMessagesFunc(model, messages, tools)
}
