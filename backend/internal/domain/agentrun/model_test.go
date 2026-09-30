package agentrun_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func entryID(id string) *conversation.EntryID {
	value := conversation.EntryID(id)
	return &value
}

func pendingRun() agentrun.Run {
	return agentrun.Run{
		ID: "r_1", ConversationID: "c_1", ClientMessageID: "client_1",
		UserMessageID: "m_user", AssistantMessageID: "m_assistant",
		UserEntryID: "e_user", LastEntryID: "e_user", LastClosedEntryID: "e_user",
		HeadVersion: 1, Status: agentrun.StatusPending,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func runningRun() agentrun.Run {
	r := pendingRun()
	r.Status = agentrun.StatusRunning
	started := r.CreatedAt.Add(time.Second)
	r.StartedAt = &started
	return r
}

func waitingRun() agentrun.Run {
	r := runningRun()
	r.Status = agentrun.StatusWaitingTool
	r.LastEntryID = "e_calls"
	r.HeadVersion = 2
	return r
}

func terminalRun(status agentrun.Status, rewind bool) agentrun.Run {
	r := runningRun()
	r.Status = status
	finished := r.CreatedAt.Add(2 * time.Second)
	r.FinishedAt = &finished
	if status == agentrun.StatusCompleted {
		r.LastEntryID, r.LastClosedEntryID = "e_final", "e_final"
		r.FinalEntryID = entryID("e_final")
		r.HeadVersion = 2
	} else if status == agentrun.StatusFailed {
		r.ErrorCode = "model_failed"
	}
	if rewind {
		r.LastEntryID = "e_calls"
		r.HeadVersion = 3 // version after rewind, NOT e_calls creation version.
	}
	return r
}

func TestValidateRunAcceptsLifecycleSnapshots(t *testing.T) {
	closed := runningRun()
	closed.LastEntryID, closed.LastClosedEntryID = "e_result_2", "e_result_2"
	closed.HeadVersion = 4
	startFailed := terminalRun(agentrun.StatusFailed, false)
	startFailed.StartedAt = nil
	startFailed.ErrorCode = "generation_start_failed"
	pendingCancelled := terminalRun(agentrun.StatusCancelled, false)
	pendingCancelled.StartedAt = nil
	nilBaseAfterReset := pendingRun()
	nilBaseAfterReset.BaseHeadVersion, nilBaseAfterReset.HeadVersion = 7, 8
	existingBase := pendingRun()
	existingBase.BaseEntryID = entryID("e_previous")
	existingBase.BaseHeadVersion, existingBase.HeadVersion = 5, 6

	cases := map[string]agentrun.Run{
		"pending": pendingRun(), "running": runningRun(), "waiting": waitingRun(),
		"closed results": closed, "completed": terminalRun(agentrun.StatusCompleted, false),
		"failed closed":     terminalRun(agentrun.StatusFailed, false),
		"failed rewound":    terminalRun(agentrun.StatusFailed, true),
		"cancelled closed":  terminalRun(agentrun.StatusCancelled, false),
		"cancelled rewound": terminalRun(agentrun.StatusCancelled, true),
		"start failed":      startFailed, "pending cancelled": pendingCancelled,
		"nil base after reset": nilBaseAfterReset, "existing base": existingBase,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			if err := agentrun.ValidateRun(run); err != nil {
				t.Fatalf("valid snapshot rejected: %v", err)
			}
		})
	}
}

func TestValidateRunRejectsInvalidSnapshots(t *testing.T) {
	cases := []struct {
		name   string
		run    agentrun.Run
		mutate func(*agentrun.Run)
	}{
		{"missing run id", pendingRun(), func(r *agentrun.Run) { r.ID = "" }},
		{"missing owner", pendingRun(), func(r *agentrun.Run) { r.ConversationID = " " }},
		{"missing client", pendingRun(), func(r *agentrun.Run) { r.ClientMessageID = "" }},
		{"missing user message", pendingRun(), func(r *agentrun.Run) { r.UserMessageID = "" }},
		{"missing assistant", pendingRun(), func(r *agentrun.Run) { r.AssistantMessageID = "" }},
		{"same message pair", pendingRun(), func(r *agentrun.Run) { r.AssistantMessageID = r.UserMessageID }},
		{"missing user entry", pendingRun(), func(r *agentrun.Run) { r.UserEntryID = "" }},
		{"missing last entry", pendingRun(), func(r *agentrun.Run) { r.LastEntryID = "" }},
		{"missing closed entry", pendingRun(), func(r *agentrun.Run) { r.LastClosedEntryID = "" }},
		{"unknown status", pendingRun(), func(r *agentrun.Run) { r.Status = "paused" }},
		{"negative base", pendingRun(), func(r *agentrun.Run) { r.BaseHeadVersion = -1 }},
		{"head not advanced", pendingRun(), func(r *agentrun.Run) { r.HeadVersion = 0 }},
		{"empty base pointer", pendingRun(), func(r *agentrun.Run) { r.BaseEntryID = entryID("") }},
		{"base version zero", pendingRun(), func(r *agentrun.Run) { r.BaseEntryID = entryID("e_base") }},
		{"base is user", pendingRun(), func(r *agentrun.Run) { r.BaseEntryID = entryID("e_user"); r.BaseHeadVersion = 1; r.HeadVersion = 2 }},
		{"missing creation", pendingRun(), func(r *agentrun.Run) { r.CreatedAt = time.Time{} }},
		{"active has finish", runningRun(), func(r *agentrun.Run) { at := r.CreatedAt; r.FinishedAt = &at }},
		{"active has final", runningRun(), func(r *agentrun.Run) { r.FinalEntryID = entryID("e_final") }},
		{"active has error", runningRun(), func(r *agentrun.Run) { r.ErrorCode = "model_failed" }},
		{"pending already started", pendingRun(), func(r *agentrun.Run) { at := r.CreatedAt; r.StartedAt = &at }},
		{"pending last differs", pendingRun(), func(r *agentrun.Run) { r.LastEntryID = "e_other" }},
		{"pending closed differs", pendingRun(), func(r *agentrun.Run) { r.LastClosedEntryID = "e_other" }},
		{"pending version advanced twice", pendingRun(), func(r *agentrun.Run) { r.HeadVersion = 2 }},
		{"running has no start", runningRun(), func(r *agentrun.Run) { r.StartedAt = nil }},
		{"waiting has no start", waitingRun(), func(r *agentrun.Run) { r.StartedAt = nil }},
		{"start is zero", runningRun(), func(r *agentrun.Run) { at := time.Time{}; r.StartedAt = &at }},
		{"running path open", runningRun(), func(r *agentrun.Run) { r.LastEntryID = "e_calls"; r.HeadVersion = 2 }},
		{"waiting path closed", waitingRun(), func(r *agentrun.Run) { r.LastClosedEntryID = r.LastEntryID }},
		{"terminal missing finish", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.FinishedAt = nil }},
		{"finish is zero", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { at := time.Time{}; r.FinishedAt = &at }},
		{"waiting version too small", waitingRun(), func(r *agentrun.Run) { r.HeadVersion = 1 }},
		{"completed version too small", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.HeadVersion = 1 }},
		{"initial user version advanced", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.HeadVersion = 2 }},
		{"unstarted failed with later fact", terminalRun(agentrun.StatusFailed, true), func(r *agentrun.Run) { r.StartedAt = nil }},
		{"unstarted cancelled with later fact", terminalRun(agentrun.StatusCancelled, true), func(r *agentrun.Run) { r.StartedAt = nil }},
		{"failed has no classification", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.ErrorCode = "" }},
		{"failed has final", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.FinalEntryID = entryID("e_final") }},
		{"cancelled has final", terminalRun(agentrun.StatusCancelled, false), func(r *agentrun.Run) { r.FinalEntryID = entryID("e_final") }},
		{"completed has no start", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.StartedAt = nil }},
		{"completed has no final", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.FinalEntryID = nil }},
		{"completed wrong final", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.FinalEntryID = entryID("e_other") }},
		{"completed wrong closed", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.LastClosedEntryID = r.UserEntryID }},
		{"completed final is user", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) {
			r.LastEntryID = r.UserEntryID
			r.LastClosedEntryID = r.UserEntryID
			r.FinalEntryID = entryID("e_user")
		}},
		{"completed has error", terminalRun(agentrun.StatusCompleted, false), func(r *agentrun.Run) { r.ErrorCode = "model_failed" }},
		{"raw error", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.ErrorCode = "Authorization: secret-value" }},
		{"oversized code", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.ErrorCode = strings.Repeat("a", 65) }},
		{"numeric code", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.ErrorCode = "500" }},
		{"initial last with other closed", terminalRun(agentrun.StatusFailed, false), func(r *agentrun.Run) { r.LastClosedEntryID = "e_other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.run.Clone()
			tc.mutate(&r)
			err := agentrun.ValidateRun(r)
			if !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Fatalf("invalid snapshot must be rejected, got %v", err)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatal("validation error leaked raw error text")
			}
		})
	}
}

func TestValidateRunDoesNotOrderPersistedWallClocks(t *testing.T) {
	r := terminalRun(agentrun.StatusCompleted, false)
	started, finished := r.CreatedAt.Add(-time.Second), r.CreatedAt.Add(-2*time.Second)
	r.StartedAt, r.FinishedAt = &started, &finished
	if err := agentrun.ValidateRun(r); err != nil {
		t.Fatalf("clock correction must not invalidate lifecycle: %v", err)
	}
}

func TestRunCloneOwnsPointers(t *testing.T) {
	r := terminalRun(agentrun.StatusCompleted, false)
	r.BaseEntryID = entryID("e_base")
	r.BaseHeadVersion, r.HeadVersion = 4, 6
	cp := r.Clone()
	*cp.BaseEntryID, *cp.FinalEntryID = "e_changed_base", "e_changed_final"
	*cp.StartedAt, *cp.FinishedAt = time.Time{}, time.Time{}
	if *r.BaseEntryID != "e_base" || *r.FinalEntryID != "e_final" || r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		t.Fatal("mutating a clone changed the source Run")
	}
	pending := pendingRun().Clone()
	if pending.BaseEntryID != nil || pending.FinalEntryID != nil || pending.StartedAt != nil || pending.FinishedAt != nil {
		t.Fatal("Clone changed nil pointer semantics")
	}
}

func TestActiveHeadTokenIsDetachedAndTerminalSafe(t *testing.T) {
	for _, r := range []agentrun.Run{pendingRun(), runningRun(), waitingRun()} {
		token, err := r.ActiveHeadToken()
		if err != nil || token.ActiveLeafID == nil || *token.ActiveLeafID != r.LastEntryID || token.Version != r.HeadVersion {
			t.Fatalf("wrong active token: %+v, %v", token, err)
		}
		*token.ActiveLeafID = "e_changed"
		if r.LastEntryID == "e_changed" {
			t.Fatal("token aliases source Run")
		}
	}
	for _, status := range []agentrun.Status{agentrun.StatusCompleted, agentrun.StatusFailed, agentrun.StatusCancelled} {
		r := terminalRun(status, status != agentrun.StatusCompleted)
		token, err := r.ActiveHeadToken()
		if !errors.Is(err, domainerr.ErrConflict) || token.ActiveLeafID != nil || token.Version != 0 {
			t.Fatalf("terminal Run exposed writable token: %+v, %v", token, err)
		}
	}
	invalid := pendingRun()
	invalid.UserEntryID = ""
	badVersion := waitingRun()
	badVersion.HeadVersion = 1
	for _, r := range []agentrun.Run{invalid, badVersion} {
		token, err := r.ActiveHeadToken()
		if !errors.Is(err, domainerr.ErrInvalidInput) || token.ActiveLeafID != nil || token.Version != 0 {
			t.Fatalf("invalid active Run exposed a token: %+v, %v", token, err)
		}
	}
}

func TestNewRunID(t *testing.T) {
	seen := make(map[agentrun.RunID]bool)
	for range 100 {
		id, err := agentrun.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(id), "r_") || len(id) != 34 || seen[id] {
			t.Fatalf("invalid or repeated Run ID: %q", id)
		}
		seen[id] = true
	}
}
