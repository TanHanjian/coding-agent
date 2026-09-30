package chat

import (
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// Request 是一次聊天生成所需的服务端可信输入。History 必须从 SQLite 加载，
// 调用方不得采用客户端传入的历史消息。
type Request struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	AssistantMessage conversation.Message
	// RunID and Recorder are required for a production new Run after wiring
	// migration. Contract-only/legacy paths currently leave them empty.
	RunID    agentrun.RunID
	Recorder RunRecorder
	// InterviewContext is optional runtime-only context used by Agent
	// adapters. HTTP callers leave it empty; evaluation and future trusted
	// application callers may provide already-sanitized material.
	InterviewContext string
}

// RuntimeInput 是一次 Graph 运行所需的服务端可信输入。
type RuntimeInput struct {
	History             []*schema.Message
	Query               string
	InterviewContext    string
	ConversationSummary string
}

// BuildInput 包含 Eino Agent 可使用的已持久化上下文。
type BuildInput struct {
	RunID               agentrun.RunID
	Recorder            RunRecorder
	Conversation        conversation.Conversation
	History             []conversation.Message
	UserMessage         conversation.Message
	InterviewContext    string
	ConversationSummary string
}

type BeginTurnInput struct {
	ConversationID  string
	ClientMessageID string
	Text            string
}

type BeginTurnResult struct {
	Conversation     conversation.Conversation
	History          []conversation.Message
	UserMessage      conversation.Message
	AssistantMessage conversation.Message
	Reused           bool
	// Run is nil only for a reused legacy Message pair after integration.
	// New turns atomically create a user Entry, Message pair and pending Run.
	Run *agentrun.Run
}

func (r BeginTurnResult) Clone() BeginTurnResult {
	cp := r
	if r.History != nil {
		cp.History = make([]conversation.Message, len(r.History))
		copy(cp.History, r.History)
	}
	if r.Run != nil {
		run := r.Run.Clone()
		cp.Run = &run
	}
	return cp
}

type CommitRunMessagesInput struct {
	RunID        agentrun.RunID
	ExpectedHead conversation.HeadToken
	Messages     []conversation.EntryMessage
}

func (input CommitRunMessagesInput) Clone() CommitRunMessagesInput {
	cp := input
	cp.ExpectedHead = input.ExpectedHead.Clone()
	if input.Messages != nil {
		cp.Messages = make([]conversation.EntryMessage, len(input.Messages))
		for i, message := range input.Messages {
			cp.Messages[i] = message.Clone()
		}
	}
	return cp
}

type CommitRunMessagesResult struct {
	Run     agentrun.Run
	Entries []conversation.ConversationEntry
	Head    conversation.EntryHead
}

func (r CommitRunMessagesResult) Clone() CommitRunMessagesResult {
	cp := r
	cp.Run = r.Run.Clone()
	cp.Head = r.Head.Clone()
	if r.Entries != nil {
		cp.Entries = make([]conversation.ConversationEntry, len(r.Entries))
		for i, entry := range r.Entries {
			cp.Entries[i] = entry.Clone()
		}
	}
	return cp
}

type FinishAssistantInput struct {
	AssistantMessageID string
	Status             conversation.MessageStatus
	ErrorCode          string
	// ErrorMessage must come from the application's controlled code-to-text
	// mapping, never an upstream error string.
	ErrorMessage string
	// Every active new Run requires the caller's latest committed token for
	// ANY terminal write. nil is permitted only for a no-write legacy/already
	// terminal retry, not as a switch to bypass CAS or read a fresher DB token.
	ExpectedHead *conversation.HeadToken
	// Only completed accepts/requires the full no-call assistant response.
	// Failed/cancelled must not persist partial responses. The cumulative
	// visible Message.Content is never a substitute for this complete message.
	FinalMessage *conversation.EntryMessage
}

func (input FinishAssistantInput) Clone() FinishAssistantInput {
	cp := input
	if input.ExpectedHead != nil {
		head := input.ExpectedHead.Clone()
		cp.ExpectedHead = &head
	}
	if input.FinalMessage != nil {
		message := input.FinalMessage.Clone()
		cp.FinalMessage = &message
	}
	return cp
}

type RecoveryResult struct {
	InterruptedRuns    int
	LegacyOnlyMessages int
}
