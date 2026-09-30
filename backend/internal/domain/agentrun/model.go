package agentrun

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

type RunID string

// Run contains references and execution state only. Entry Tree owns complete
// message facts; legacy messages own the visible streaming text.
type Run struct {
	ID                 RunID
	ConversationID     string
	ClientMessageID    string
	UserMessageID      string
	AssistantMessageID string

	BaseEntryID       *conversation.EntryID
	BaseHeadVersion   int64
	UserEntryID       conversation.EntryID
	LastEntryID       conversation.EntryID
	LastClosedEntryID conversation.EntryID
	FinalEntryID      *conversation.EntryID
	HeadVersion       int64

	Status     Status
	ErrorCode  string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

func NewRunID() (RunID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return RunID("r_" + hex.EncodeToString(bytes[:])), nil
}

func (r Run) Clone() Run {
	cp := r
	if r.BaseEntryID != nil {
		id := *r.BaseEntryID
		cp.BaseEntryID = &id
	}
	if r.FinalEntryID != nil {
		id := *r.FinalEntryID
		cp.FinalEntryID = &id
	}
	if r.StartedAt != nil {
		at := *r.StartedAt
		cp.StartedAt = &at
	}
	if r.FinishedAt != nil {
		at := *r.FinishedAt
		cp.FinishedAt = &at
	}
	return cp
}

// ActiveHeadToken is available only for an active, structurally valid Run.
// After failure rewind, LastEntryID remains the last fact while HeadVersion
// describes the rewound head: that pair must never become a writable token.
func (r Run) ActiveHeadToken() (conversation.HeadToken, error) {
	if IsTerminal(r.Status) {
		return conversation.HeadToken{}, fmt.Errorf("%w: terminal run has no writable head", domainerr.ErrConflict)
	}
	if err := ValidateRun(r); err != nil {
		return conversation.HeadToken{}, err
	}
	id := r.LastEntryID
	return conversation.HeadToken{ActiveLeafID: &id, Version: r.HeadVersion}, nil
}

// ValidateRun checks local structural invariants. It cannot prove ownership,
// ancestry, message roles, the latest closed point, or agreement with the DB
// head. GenerationStore must validate those facts in its write transaction.
// ErrorCode is a classification, not arbitrary error text; its approved codes
// and safe display messages belong to the application mapping at integration.
func ValidateRun(r Run) error {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s", domainerr.ErrInvalidInput, reason)
	}
	for _, id := range []string{
		string(r.ID), r.ConversationID, r.ClientMessageID, r.UserMessageID,
		r.AssistantMessageID, string(r.UserEntryID), string(r.LastEntryID), string(r.LastClosedEntryID),
	} {
		if strings.TrimSpace(id) == "" {
			return invalid("run identity and references are required")
		}
	}
	if r.UserMessageID == r.AssistantMessageID {
		return invalid("run message pair must be distinct")
	}
	if !IsActive(r.Status) && !IsTerminal(r.Status) {
		return invalid("unknown run status")
	}
	if r.BaseHeadVersion < 0 || r.HeadVersion <= r.BaseHeadVersion {
		return invalid("run head must advance beyond its base")
	}
	if r.BaseEntryID != nil && (strings.TrimSpace(string(*r.BaseEntryID)) == "" || *r.BaseEntryID == r.UserEntryID || r.BaseHeadVersion == 0) {
		return invalid("invalid run base reference")
	}
	// A nil base with a positive version is valid after an internal head reset.
	if r.CreatedAt.IsZero() {
		return invalid("run creation time is required")
	}
	// Persisted wall clocks can move backwards across clock correction or
	// restart. State and head version, not timestamp ordering, order writes.
	if r.StartedAt != nil && r.StartedAt.IsZero() {
		return invalid("invalid run start time")
	}
	if IsActive(r.Status) {
		if r.FinishedAt != nil || r.FinalEntryID != nil || r.ErrorCode != "" {
			return invalid("active run cannot have terminal metadata")
		}
	} else {
		if r.FinishedAt == nil || r.FinishedAt.IsZero() {
			return invalid("terminal run requires a valid finish time")
		}
	}
	if r.Status == StatusRunning || r.Status == StatusWaitingTool || r.Status == StatusCompleted {
		if r.StartedAt == nil {
			return invalid("started run requires start time")
		}
	}
	if r.LastEntryID == r.UserEntryID {
		if r.LastClosedEntryID != r.UserEntryID || r.HeadVersion-r.BaseHeadVersion != 1 {
			return invalid("initial user must be the unchanged closed point")
		}
	} else {
		// A later fact needs at least the user append and another append.
		// The exact number of appends/rewinds is verified in the transaction.
		if r.HeadVersion-r.BaseHeadVersion < 2 {
			return invalid("later run fact requires another head advance")
		}
		if r.StartedAt == nil {
			return invalid("later run fact requires start time")
		}
	}
	switch r.Status {
	case StatusPending:
		if r.StartedAt != nil || r.LastEntryID != r.UserEntryID || r.LastClosedEntryID != r.UserEntryID || r.HeadVersion-r.BaseHeadVersion != 1 {
			return invalid("invalid pending run references")
		}
	case StatusRunning:
		if r.LastClosedEntryID != r.LastEntryID {
			return invalid("running run must have a closed message path")
		}
	case StatusWaitingTool:
		if r.LastEntryID == r.LastClosedEntryID {
			return invalid("waiting run must have an open tool group")
		}
	case StatusCompleted:
		if r.FinalEntryID == nil || *r.FinalEntryID != r.LastEntryID || r.LastClosedEntryID != r.LastEntryID || r.LastEntryID == r.UserEntryID || r.ErrorCode != "" {
			return invalid("completed run requires a closed final response")
		}
	case StatusFailed, StatusCancelled:
		if r.FinalEntryID != nil {
			return invalid("unsuccessful run cannot have a final entry")
		}
		if r.Status == StatusFailed && r.ErrorCode == "" {
			return invalid("failed run requires an error classification")
		}
	}
	if r.ErrorCode != "" && !validErrorCode(r.ErrorCode) {
		return invalid("invalid run error classification")
	}
	return nil
}

func validErrorCode(code string) bool {
	if len(code) > 64 || code[0] < 'a' || code[0] > 'z' {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
