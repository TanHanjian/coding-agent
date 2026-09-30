package chat

import (
	"context"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/agentrun"
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
	ToolTitle  string
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

// TurnStore remains the legacy production port during the contract-only slice.
// It must not be treated as a GenerationStore or used to bypass Run persistence
// once production wiring switches to the new lifecycle.
type TurnStore interface {
	BeginTurn(context.Context, BeginTurnInput) (BeginTurnResult, error)
	AppendAssistantText(context.Context, string, string) error
	FinishAssistant(context.Context, FinishAssistantInput) (conversation.Message, error)
	GetMessage(context.Context, string) (conversation.Message, error)
}

// GenerationStore owns the atomic Run/Entry/legacy Message write boundary.
// BeginTurn checks Run/legacy idempotency before busy and never backfills a
// legacy pair. FinishAssistant returns an identical terminal retry before any
// active/head validation; a different terminal intent conflicts. New terminal
// writes require the caller's committed token, never a refreshed database token.
// Each operation returns only after its transaction commits; persistence errors
// must not publish Hub events or advance a Recorder's head token.
type GenerationStore interface {
	TurnStore
	// MarkRunning allows only pending -> running, before Executor starts.
	MarkRunning(context.Context, agentrun.RunID) (agentrun.Run, error)
	// CommitRunMessages accepts one complete assistant call message while
	// running, or the entire pending tool result group while waiting_tool.
	// Results are separate Entries committed atomically in declaration order.
	// It is not a generic batch append or status mutation interface.
	CommitRunMessages(context.Context, CommitRunMessagesInput) (CommitRunMessagesResult, error)
}

// RunRecorder is a private, serialized full-message control seam for one Run.
// A complete assistant call must commit before tools run; a complete result
// group must commit before the next model call. A no-call final response stays
// in memory until FinishAssistant atomically commits it with terminal state.
// Implementations clone inputs, propagate errors, and never update their token
// on a failed commit. Raw provider/tool payloads must not enter this interface.
// Production new Runs require a Recorder; an offline no-persistence mode must
// be explicitly selected rather than interpreting nil as successful recording.
type RunRecorder interface {
	RecordAssistant(context.Context, conversation.EntryMessage) error
	RecordToolResults(context.Context, []conversation.EntryMessage) error
}

// RecoveryStore is deliberately separate from ordinary generation writes.
// Callers must hold exclusive process ownership of the database and must not
// accept requests before successful recovery. Recovery is one transaction,
// including controlled head rewinds and legacy-only streaming convergence;
// it never resumes execution, invents Entry facts, or replays tools.
type RecoveryStore interface {
	RecoverInterrupted(context.Context) (RecoveryResult, error)
}

// Runtime emits final assistant text from the Eino Graph. Tool calls remain
// Graph-internal; Executor observes their node callbacks separately.
type Runtime interface {
	Stream(context.Context, RuntimeInput, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

type RuntimeBuilder interface {
	Build(context.Context, BuildInput) (Runtime, error)
}
