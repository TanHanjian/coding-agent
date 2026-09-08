package question

import "interview-memory-agent/backend/internal/domain/domainerr"

// Kept as aliases while callers migrate to domainerr.
var (
	ErrNotImplemented = domainerr.ErrNotImplemented
	ErrNotFound       = domainerr.ErrNotFound
	ErrInvalidInput   = domainerr.ErrInvalidInput
)
