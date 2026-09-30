package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type EntryID string
type EntryKind string

const (
	EntryKindMessage EntryKind = "message"
)

const EntryPayloadV1 = 1

type ConversationEntry struct {
	ID             EntryID      `json:"id"`
	ConversationID string       `json:"conversationId"`
	ParentID       *EntryID     `json:"parentId"`
	Kind           EntryKind    `json:"kind"`
	PayloadVersion int          `json:"payloadVersion"`
	Message        EntryMessage `json:"message"`
	AppendIndex    int64        `json:"appendIndex"`
	Depth          int64        `json:"depth"`
	CreatedAt      time.Time    `json:"createdAt"`
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
	cp.Message = e.Message.Clone()
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
