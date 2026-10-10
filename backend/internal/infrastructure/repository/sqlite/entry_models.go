package sqlite

import (
	"fmt"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// entryNodeRow preserves the existing TEXT payload and timestamp storage format.
// Nullable references remain pointers; rows have no associations or ORM clocks.
type entryNodeRow struct {
	ID             string  `gorm:"column:id;primaryKey"`
	ConversationID string  `gorm:"column:conversation_id"`
	ParentID       *string `gorm:"column:parent_id"`
	Kind           string  `gorm:"column:kind"`
	PayloadVersion int     `gorm:"column:payload_version"`
	PayloadJSON    string  `gorm:"column:payload_json;type:text"`
	AppendIndex    int64   `gorm:"column:append_index"`
	Depth          int64   `gorm:"column:depth"`
	CreatedAt      string  `gorm:"column:created_at;autoCreateTime:false;autoUpdateTime:false"`
}

func (entryNodeRow) TableName() string { return "conversation_entry_nodes" }

func (row entryNodeRow) toEntry() (conversation.ConversationEntry, error) {
	createdAt, err := parseRFC3339(row.CreatedAt)
	if err != nil {
		return conversation.ConversationEntry{}, fmt.Errorf("%w: corrupt created_at for entry %s: %v", conversation.ErrEntryCorrupt, row.ID, err)
	}

	var pidPtr *conversation.EntryID
	if row.ParentID != nil {
		pid := conversation.EntryID(*row.ParentID)
		pidPtr = &pid
	}

	entry := conversation.ConversationEntry{
		ID:             conversation.EntryID(row.ID),
		ConversationID: row.ConversationID,
		ParentID:       pidPtr,
		Kind:           conversation.EntryKind(row.Kind),
		PayloadVersion: row.PayloadVersion,
		AppendIndex:    row.AppendIndex,
		Depth:          row.Depth,
		CreatedAt:      createdAt,
	}

	// Decode payload based on kind
	switch conversation.EntryKind(row.Kind) {
	case conversation.EntryKindMessage:
		msg, err := DecodeEntryMessage(row.PayloadVersion, []byte(row.PayloadJSON))
		if err != nil {
			return conversation.ConversationEntry{}, err
		}
		entry.Message = &msg
	case conversation.EntryKindCompaction:
		compaction, err := DecodeEntryCompaction(row.PayloadVersion, []byte(row.PayloadJSON))
		if err != nil {
			return conversation.ConversationEntry{}, err
		}
		entry.Compaction = &compaction
	default:
		return conversation.ConversationEntry{}, fmt.Errorf("%w: unsupported entry kind %s", conversation.ErrEntryCorrupt, row.Kind)
	}

	return entry, nil
}

type entryHeadRow struct {
	ConversationID string  `gorm:"column:conversation_id;primaryKey"`
	ActiveLeafID   *string `gorm:"column:active_leaf_id"`
	Version        int64   `gorm:"column:version"`
	UpdatedAt      string  `gorm:"column:updated_at;autoCreateTime:false;autoUpdateTime:false"`
}

func (entryHeadRow) TableName() string { return "conversation_entry_heads" }

func (row entryHeadRow) toHead() conversation.EntryHead {
	updatedAt, _ := parseRFC3339(row.UpdatedAt)
	var leaf *conversation.EntryID
	if row.ActiveLeafID != nil {
		id := conversation.EntryID(*row.ActiveLeafID)
		leaf = &id
	}
	return conversation.EntryHead{
		ConversationID: row.ConversationID,
		ActiveLeafID:   leaf,
		Version:        row.Version,
		UpdatedAt:      updatedAt,
	}
}

// entryAppendIndexRow represents MAX's NULL result for an empty tree.
type entryAppendIndexRow struct {
	MaxIndex *int64 `gorm:"column:max_index"`
}
