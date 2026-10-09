package sqlite

import (
	"time"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// runRow mirrors agent_runs without associations or ORM-managed clocks.
type runRow struct {
	ID                 string  `gorm:"column:id;primaryKey"`
	ConversationID     string  `gorm:"column:conversation_id"`
	ClientMessageID    string  `gorm:"column:client_message_id"`
	UserMessageID      string  `gorm:"column:user_message_id"`
	AssistantMessageID string  `gorm:"column:assistant_message_id"`
	BaseEntryID        *string `gorm:"column:base_entry_id"`
	BaseHeadVersion    int64   `gorm:"column:base_head_version"`
	UserEntryID        string  `gorm:"column:user_entry_id"`
	LastEntryID        string  `gorm:"column:last_entry_id"`
	LastClosedEntryID  string  `gorm:"column:last_closed_entry_id"`
	FinalEntryID       *string `gorm:"column:final_entry_id"`
	HeadVersion        int64   `gorm:"column:head_version"`
	Status             string  `gorm:"column:status"`
	ErrorCode          string  `gorm:"column:error_code"`
	CreatedAt          string  `gorm:"column:created_at;autoCreateTime:false"`
	StartedAt          *string `gorm:"column:started_at;autoCreateTime:false;autoUpdateTime:false"`
	FinishedAt         *string `gorm:"column:finished_at;autoCreateTime:false;autoUpdateTime:false"`
}

func (runRow) TableName() string { return "agent_runs" }

func runToRow(run agentrun.Run) runRow {
	row := runRow{
		ID: string(run.ID), ConversationID: run.ConversationID, ClientMessageID: run.ClientMessageID,
		UserMessageID: run.UserMessageID, AssistantMessageID: run.AssistantMessageID,
		BaseHeadVersion: run.BaseHeadVersion, UserEntryID: string(run.UserEntryID),
		LastEntryID: string(run.LastEntryID), LastClosedEntryID: string(run.LastClosedEntryID),
		HeadVersion: run.HeadVersion, Status: string(run.Status), ErrorCode: run.ErrorCode,
		CreatedAt: formatTime(run.CreatedAt),
	}
	if run.BaseEntryID != nil {
		id := string(*run.BaseEntryID)
		row.BaseEntryID = &id
	}
	if run.FinalEntryID != nil {
		id := string(*run.FinalEntryID)
		row.FinalEntryID = &id
	}
	if run.StartedAt != nil {
		at := formatTime(*run.StartedAt)
		row.StartedAt = &at
	}
	if run.FinishedAt != nil {
		at := formatTime(*run.FinishedAt)
		row.FinishedAt = &at
	}
	return row
}

func (row runRow) toRun() (agentrun.Run, error) {
	run := agentrun.Run{
		ID: agentrun.RunID(row.ID), ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		UserMessageID: row.UserMessageID, AssistantMessageID: row.AssistantMessageID,
		BaseHeadVersion: row.BaseHeadVersion, UserEntryID: conversation.EntryID(row.UserEntryID),
		LastEntryID: conversation.EntryID(row.LastEntryID), LastClosedEntryID: conversation.EntryID(row.LastClosedEntryID),
		HeadVersion: row.HeadVersion, Status: agentrun.Status(row.Status), ErrorCode: row.ErrorCode,
	}
	if row.BaseEntryID != nil {
		id := conversation.EntryID(*row.BaseEntryID)
		run.BaseEntryID = &id
	}
	if row.FinalEntryID != nil {
		id := conversation.EntryID(*row.FinalEntryID)
		run.FinalEntryID = &id
	}
	var err error
	if run.CreatedAt, err = parseRFC3339(row.CreatedAt); err != nil {
		return agentrun.Run{}, corruptRun("invalid creation timestamp")
	}
	for _, field := range []struct {
		raw *string
		dst **time.Time
	}{{row.StartedAt, &run.StartedAt}, {row.FinishedAt, &run.FinishedAt}} {
		if field.raw != nil {
			at, err := parseRFC3339(*field.raw)
			if err != nil {
				return agentrun.Run{}, corruptRun("invalid lifecycle timestamp")
			}
			*field.dst = &at
		}
	}
	if err := agentrun.ValidateRun(run); err != nil {
		return agentrun.Run{}, corruptRun("invalid run structure: %v", err)
	}
	return run, nil
}
