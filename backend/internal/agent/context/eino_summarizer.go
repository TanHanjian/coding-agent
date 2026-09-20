package agentcontext

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/components/model"
)

// EinoContextSummarizer is the Eino adapter seam for structured conversation
// summarization. Prompt construction, strict JSON decoding, retries, and
// timeout policy are intentionally left for the implementation phase.
type EinoContextSummarizer struct {
	chatModel model.BaseChatModel
}

func NewEinoContextSummarizer(chatModel model.BaseChatModel) (*EinoContextSummarizer, error) {
	if chatModel == nil {
		return nil, errors.New("agent context: chat model is required for summarizer")
	}
	return &EinoContextSummarizer{chatModel: chatModel}, nil
}

func (*EinoContextSummarizer) Summarize(context.Context, SummaryPrompt) (ContextSummary, error) {
	return ContextSummary{}, ErrContextSummarizerNotImplemented
}

var _ ContextSummarizer = (*EinoContextSummarizer)(nil)
