package conversation

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrEntryCorrupt            = errors.New("conversation entry corrupt")
	ErrEntryUnsupportedVersion = errors.New("conversation entry unsupported version")
	ErrEntryCapacity           = errors.New("conversation entry capacity exceeded")
)

type EntryErrorCode string

const (
	EntryErrCodeInvalidInput       EntryErrorCode = "invalid_input"
	EntryErrCodeNotFound           EntryErrorCode = "not_found"
	EntryErrCodeConflict           EntryErrorCode = "conflict"
	EntryErrCodeCorrupt            EntryErrorCode = "corrupt"
	EntryErrCodeUnsupportedVersion EntryErrorCode = "unsupported_version"
	EntryErrCodeCapacity           EntryErrorCode = "capacity_exceeded"
)

type EntryError struct {
	Code    EntryErrorCode
	EntryID EntryID
	Reason  string
	Err     error
}

func (e *EntryError) Error() string {
	if e == nil {
		return ""
	}
	if e.EntryID != "" {
		return fmt.Sprintf("entry %s [%s]: %s", e.EntryID, e.Code, e.Reason)
	}
	return fmt.Sprintf("entry [%s]: %s", e.Code, e.Reason)
}

func (e *EntryError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewEntryError(code EntryErrorCode, entryID EntryID, reason string, baseErr error) *EntryError {
	return &EntryError{
		Code:    code,
		EntryID: entryID,
		Reason:  reason,
		Err:     baseErr,
	}
}

type EntryTree interface {
	GetHead(ctx context.Context, conversationID string) (EntryHead, error)
	Get(ctx context.Context, ref EntryRef) (ConversationEntry, error)
	ReadPath(ctx context.Context, query PathQuery) (EntryPath, error)
	ListChildren(ctx context.Context, query ChildrenQuery) (ChildrenPage, error)
	Append(ctx context.Context, input AppendEntryInput) (AppendEntryResult, error)
	MoveHead(ctx context.Context, input MoveHeadInput) (EntryHead, error)
}

type EntryRef struct {
	ConversationID string
	ID             EntryID
}

type PathQuery struct {
	ConversationID string
	LeafID         *EntryID
	UseActiveLeaf  bool
}

type EntryPath struct {
	Head    EntryHead
	LeafID  *EntryID
	Entries []ConversationEntry
}

type ChildrenQuery struct {
	ConversationID string
	ParentID       *EntryID
	AfterIndex     int64
	Limit          int
}

type ChildrenPage struct {
	Entries   []ConversationEntry
	NextIndex *int64
}

type AppendEntryInput struct {
	ConversationID string
	ExpectedHead   HeadToken
	Message        EntryMessage
}

type AppendEntryResult struct {
	Entry ConversationEntry
	Head  EntryHead
}

type MoveHeadInput struct {
	ConversationID string
	ExpectedHead   HeadToken
	TargetID       *EntryID
}
