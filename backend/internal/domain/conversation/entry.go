package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type EntryID string
type EntryKind string

const (
	EntryKindMessage    EntryKind = "message"
	EntryKindCompaction EntryKind = "compaction"
)

const (
	EntryPayloadV1 = 1
	CompactionPayloadV1 = 1
)

type ConversationEntry struct {
	ID             EntryID           `json:"id"`
	ConversationID string            `json:"conversationId"`
	ParentID       *EntryID          `json:"parentId"`
	Kind           EntryKind         `json:"kind"`
	PayloadVersion int               `json:"payloadVersion"`
	Message        *EntryMessage     `json:"message,omitempty"`
	Compaction     *EntryCompaction  `json:"compaction,omitempty"`
	AppendIndex    int64             `json:"appendIndex"`
	Depth          int64             `json:"depth"`
	CreatedAt      time.Time         `json:"createdAt"`
}

type EntryHead struct {
	ConversationID string    `json:"conversationId"`
	ActiveLeafID   *EntryID  `json:"activeLeafId"`
	Version        int64     `json:"version"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type HeadToken struct {
	ActiveLeafID *EntryID
	Version      int64
}

type EntryRole string

const (
	EntryRoleUser       EntryRole = "user"
	EntryRoleAssistant  EntryRole = "assistant"
	EntryRoleToolResult EntryRole = "toolResult"
)

type EntryPartType string

const (
	EntryPartText     EntryPartType = "text"
	EntryPartToolCall EntryPartType = "toolCall"
)

type EntryStopReason string

const (
	EntryStopNormal EntryStopReason = "stop"
	EntryStopTools  EntryStopReason = "toolUse"
)

type EntryMessage struct {
	Role       EntryRole        `json:"role"`
	Content    []EntryPart      `json:"content,omitempty"`
	ToolResult *EntryToolResult `json:"toolResult,omitempty"`
	StopReason EntryStopReason  `json:"stopReason,omitempty"`
}

type EntryPart struct {
	Type     EntryPartType `json:"type"`
	Text     *string       `json:"text,omitempty"`
	ToolCall *SafeToolCall `json:"toolCall,omitempty"`
}

type EntryToolResult struct {
	CallEntryID EntryID        `json:"callEntryId"`
	CallID      string         `json:"callId"`
	ToolName    string         `json:"toolName"`
	IsError     bool           `json:"isError"`
	Result      SafeToolResult `json:"result"`
}

// EntryCompaction 是压缩摘要的 payload
type EntryCompaction struct {
	Schema           string   `json:"schema"` // "structured-v1"
	FirstKeptEntryID EntryID  `json:"firstKeptEntryId"`
	SourceBoundary   []EntryID `json:"sourceBoundary"` // 被压缩的前缀 entry IDs
	TokensBefore     int      `json:"tokensBefore"`
	Summary          CompactionSummary `json:"summary"`
}

// CompactionSummary 是结构化摘要内容
type CompactionSummary struct {
	Version        int      `json:"version"`        // 新增：schema 版本
	Goal           string   `json:"goal"`
	ConfirmedFacts []string `json:"confirmedFacts"`
	Decisions      []string `json:"decisions"`
	Constraints    []string `json:"constraints"`    // 新增：约束条件
	OpenQuestions  []string `json:"openQuestions"`  // 新增：未解决问题
	Context        string   `json:"context"`        // 保留：兼容旧字段
}

func NewEntryID() (EntryID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return EntryID("e_" + hex.EncodeToString(bytes[:])), nil
}

func (e ConversationEntry) Clone() ConversationEntry {
	cp := e
	if e.ParentID != nil {
		pid := *e.ParentID
		cp.ParentID = &pid
	}
	if e.Message != nil {
		msg := e.Message.Clone()
		cp.Message = &msg
	}
	if e.Compaction != nil {
		comp := e.Compaction.Clone()
		cp.Compaction = &comp
	}
	return cp
}

func (m EntryMessage) Clone() EntryMessage {
	cp := m
	if m.Content != nil {
		cp.Content = make([]EntryPart, len(m.Content))
		for i, part := range m.Content {
			cp.Content[i] = part.Clone()
		}
	}
	if m.ToolResult != nil {
		tr := *m.ToolResult
		tr.Result = m.ToolResult.Result.Clone()
		cp.ToolResult = &tr
	}
	return cp
}

func (p EntryPart) Clone() EntryPart {
	cp := p
	if p.Text != nil {
		txt := *p.Text
		cp.Text = &txt
	}
	if p.ToolCall != nil {
		tc := p.ToolCall.Clone()
		cp.ToolCall = &tc
	}
	return cp
}

func (h EntryHead) Clone() EntryHead {
	cp := h
	if h.ActiveLeafID != nil {
		id := *h.ActiveLeafID
		cp.ActiveLeafID = &id
	}
	return cp
}

func (t HeadToken) Clone() HeadToken {
	cp := t
	if t.ActiveLeafID != nil {
		id := *t.ActiveLeafID
		cp.ActiveLeafID = &id
	}
	return cp
}

func (c EntryCompaction) Clone() EntryCompaction {
	cp := c
	if c.SourceBoundary != nil {
		cp.SourceBoundary = make([]EntryID, len(c.SourceBoundary))
		copy(cp.SourceBoundary, c.SourceBoundary)
	}
	cp.Summary = c.Summary.Clone()
	return cp
}

func (s CompactionSummary) Clone() CompactionSummary {
	cp := s
	if s.ConfirmedFacts != nil {
		cp.ConfirmedFacts = make([]string, len(s.ConfirmedFacts))
		copy(cp.ConfirmedFacts, s.ConfirmedFacts)
	}
	if s.Decisions != nil {
		cp.Decisions = make([]string, len(s.Decisions))
		copy(cp.Decisions, s.Decisions)
	}
	return cp
}
