package agentrun_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func TestLifecycleClassificationAndTransitions(t *testing.T) {
	statuses := []agentrun.Status{
		agentrun.StatusPending, agentrun.StatusRunning, agentrun.StatusWaitingTool,
		agentrun.StatusCompleted, agentrun.StatusFailed, agentrun.StatusCancelled,
	}
	allowed := map[agentrun.Status][]agentrun.Status{
		agentrun.StatusPending:     {agentrun.StatusRunning, agentrun.StatusFailed, agentrun.StatusCancelled},
		agentrun.StatusRunning:     {agentrun.StatusWaitingTool, agentrun.StatusCompleted, agentrun.StatusFailed, agentrun.StatusCancelled},
		agentrun.StatusWaitingTool: {agentrun.StatusRunning, agentrun.StatusFailed, agentrun.StatusCancelled},
	}
	for i, from := range statuses {
		if got := agentrun.IsActive(from); got != (i < 3) {
			t.Errorf("IsActive(%s) = %v", from, got)
		}
		if got := agentrun.IsTerminal(from); got != (i >= 3) {
			t.Errorf("IsTerminal(%s) = %v", from, got)
		}
		for _, to := range statuses {
			t.Run(fmt.Sprintf("%s_to_%s", from, to), func(t *testing.T) {
				wantAllowed := false
				for _, target := range allowed[from] {
					wantAllowed = wantAllowed || target == to
				}
				err := agentrun.ValidateTransition(from, to)
				if wantAllowed && err != nil {
					t.Fatalf("legal transition rejected: %v", err)
				}
				if !wantAllowed && !errors.Is(err, domainerr.ErrConflict) {
					t.Fatalf("illegal transition must conflict, got %v", err)
				}
			})
		}
	}
}

func TestUnknownStatusIsNeitherActiveNorTerminal(t *testing.T) {
	for _, status := range []agentrun.Status{"", "paused", "provider secret"} {
		if agentrun.IsActive(status) || agentrun.IsTerminal(status) {
			t.Fatalf("unknown status classified as valid: %q", status)
		}
		for _, pair := range [][2]agentrun.Status{{status, agentrun.StatusRunning}, {agentrun.StatusRunning, status}} {
			err := agentrun.ValidateTransition(pair[0], pair[1])
			if !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Fatalf("unknown status must be invalid input, got %v", err)
			}
			if status != "" && strings.Contains(err.Error(), string(status)) {
				t.Fatal("validation error exposed untrusted status")
			}
		}
	}
}
