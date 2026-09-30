// Package agentrun defines execution identity and lifecycle without message bodies.
package agentrun

import (
	"fmt"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

type Status string

const (
	StatusPending     Status = "pending"
	StatusRunning     Status = "running"
	StatusWaitingTool Status = "waiting_tool"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

func IsActive(status Status) bool {
	return status == StatusPending || status == StatusRunning || status == StatusWaitingTool
}

func IsTerminal(status Status) bool {
	return status == StatusCompleted || status == StatusFailed || status == StatusCancelled
}

// ValidateTransition checks a real state change, not an idempotent store retry.
// Stores must return an existing identical terminal result before attempting a
// transition or reconstructing a writable head token. There is no SetStatus API.
func ValidateTransition(from, to Status) error {
	if (!IsActive(from) && !IsTerminal(from)) || (!IsActive(to) && !IsTerminal(to)) {
		return fmt.Errorf("%w: unknown run status", domainerr.ErrInvalidInput)
	}

	allowed := false
	switch from {
	case StatusPending:
		allowed = to == StatusRunning || to == StatusFailed || to == StatusCancelled
	case StatusRunning:
		allowed = to == StatusWaitingTool || IsTerminal(to)
	case StatusWaitingTool:
		allowed = to == StatusRunning || to == StatusFailed || to == StatusCancelled
	}
	if !allowed {
		return fmt.Errorf("%w: run transition not allowed", domainerr.ErrConflict)
	}
	return nil
}
