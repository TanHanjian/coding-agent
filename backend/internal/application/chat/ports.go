package chat

import (
	"context"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// GenerationEvent is a transient, ordered Agent lifecycle event for the
// active browser stream. Tool payloads are never persisted as chat history.
type GenerationEvent struct {
	Kind       GenerationEventKind
	StepID     string
	Phase      string
	ToolCallID string
	ToolName   string
	Input      any
	Output     any
	ErrorText  string
}

type GenerationEventKind string

const (
	GenerationEventStepStart       GenerationEventKind = "step-start"
	GenerationEventStepFinish      GenerationEventKind = "step-finish"
	GenerationEventPhase           GenerationEventKind = "phase"
	GenerationEventToolInput       GenerationEventKind = "tool-input"
	GenerationEventToolOutput      GenerationEventKind = "tool-output"
	GenerationEventToolOutputError GenerationEventKind = "tool-output-error"
)

// TextSink receives visible assistant text plus transient Agent events.
type TextSink interface {
	WriteChunk(context.Context, string) error
	WriteEvent(context.Context, GenerationEvent) error
}

type Executor interface {
	Stream(context.Context, Request, TextSink) error
}

type TurnStore interface {
	BeginTurn(context.Context, BeginTurnInput) (BeginTurnResult, error)
	AppendAssistantText(context.Context, string, string) error
	FinishAssistant(context.Context, FinishAssistantInput) (conversation.Message, error)
	GetMessage(context.Context, string) (conversation.Message, error)
}

// Runtime emits final assistant text from the Eino Graph. Tool calls remain
// Graph-internal; Executor observes their node callbacks separately.
type Runtime interface {
	Stream(context.Context, RuntimeInput, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

type RuntimeBuilder interface {
	Build(context.Context, BuildInput) (Runtime, error)
}
