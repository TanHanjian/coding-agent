package chat

import (
	"context"

	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// ContextManager is the top-level seam for assembling one Agent run's model
// context. Callers provide trusted persisted conversation data and the current
// turn; the manager decides how history, summaries, task material, and token
// budgets are represented in the prepared context.
//
// The interface intentionally hides the lower-level policy modules. Callers
// must not select history messages, invoke a summarizer, or read the summary
// store themselves.
type ContextManager interface {
	Prepare(context.Context, ContextInput) (PreparedContext, error)
}

// ContextInput contains the trusted inputs available before the current Agent
// run starts. History is loaded by the server and must not come from a client
// request.
type ContextInput struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	InterviewContext string
}

// PreparedContext is the context package consumed by the Agent executor. The
// summary is runtime-only and is never treated as a user-visible message.
type PreparedContext struct {
	History             []conversation.Message
	Query               string
	InterviewContext    string
	ConversationSummary string
	Metadata            ContextMetadata
}

// ContextMetadata contains bounded diagnostics about context assembly. It must
// contain counts and states only; implementations must not put source text,
// tool payloads, credentials, or prompts here.
type ContextMetadata struct {
	HistoryIncluded      int
	HistoryOmitted       int
	SummaryUsed          bool
	SummaryUpdated       bool
	SummaryFallback      bool
	Truncated            bool
	EstimatedTokens      int
	ContextWindowTokens  int
	OutputReservedTokens int
}

// PassThroughContextManager preserves the current behavior while the
// lower-level context policies are introduced incrementally. It copies the
// history slice so downstream code cannot mutate the caller's slice.
type PassThroughContextManager struct{}

func (PassThroughContextManager) Prepare(_ context.Context, input ContextInput) (PreparedContext, error) {
	return PreparedContext{
		History:             cloneMessages(input.History),
		Query:               input.UserMessage.Content,
		InterviewContext:    input.InterviewContext,
		ConversationSummary: "",
		Metadata: ContextMetadata{
			HistoryIncluded: len(input.History),
		},
	}, nil
}

func cloneMessages(messages []conversation.Message) []conversation.Message {
	if messages == nil {
		return nil
	}
	return append([]conversation.Message(nil), messages...)
}

// ToRuntimeInput adapts the prepared, domain-level context to the Eino
// runtime input without exposing Eino types to ContextManager implementations.
func (p PreparedContext) ToRuntimeInput(history []*schema.Message) RuntimeInput {
	return RuntimeInput{
		History:             history,
		Query:               p.Query,
		InterviewContext:    p.InterviewContext,
		ConversationSummary: p.ConversationSummary,
	}
}

var _ ContextManager = PassThroughContextManager{}
