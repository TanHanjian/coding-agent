package sqlite

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func parseRFC3339(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func checkConversationExists(ctx context.Context, tx *gorm.DB, conversationID string) (time.Time, error) {
	var row conversationRow
	err := tx.WithContext(ctx).Select("updated_at").Where("id = ?", conversationID).Take(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return time.Time{}, domainerr.ErrNotFound
		}
		return time.Time{}, err
	}
	t, _ := parseRFC3339(row.UpdatedAt)
	return t, nil
}

func loadHeadTx(ctx context.Context, tx *gorm.DB, conversationID string) (conversation.EntryHead, bool, error) {
	var row entryHeadRow
	err := tx.WithContext(ctx).Where("conversation_id = ?", conversationID).Take(&row).Error
	if err == nil {
		return row.toHead(), true, nil
	}
	if err != gorm.ErrRecordNotFound {
		return conversation.EntryHead{}, false, err
	}

	// No head found in table: check if any nodes exist
	var count int64
	if err := tx.WithContext(ctx).Model(&entryNodeRow{}).
		Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
		return conversation.EntryHead{}, false, err
	}

	if count > 0 {
		return conversation.EntryHead{}, false, fmt.Errorf("%w: missing head for non-empty conversation tree", conversation.ErrEntryCorrupt)
	}

	return conversation.EntryHead{}, false, nil
}

func lazyInitHeadTx(ctx context.Context, tx *gorm.DB, conversationID string, now time.Time) (conversation.EntryHead, error) {
	head, exists, err := loadHeadTx(ctx, tx, conversationID)
	if err != nil {
		return conversation.EntryHead{}, err
	}
	if exists {
		return head, nil
	}

	row := entryHeadRow{
		ConversationID: conversationID,
		ActiveLeafID:   nil,
		Version:        0,
		UpdatedAt:      now.UTC().Format(time.RFC3339Nano),
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return conversation.EntryHead{}, err
	}

	return conversation.EntryHead{
		ConversationID: conversationID,
		ActiveLeafID:   nil,
		Version:        0,
		UpdatedAt:      now.UTC(),
	}, nil
}

func readPathTx(ctx context.Context, tx *gorm.DB, conversationID string, targetLeaf conversation.EntryID, maxEntries int) ([]conversation.ConversationEntry, error) {
	var rows []entryNodeRow
	result := tx.WithContext(ctx).Raw(entryPathSQL, conversationID, string(targetLeaf), maxEntries).Scan(&rows)
	if result.Error != nil {
		return nil, result.Error
	}

	path := make([]conversation.ConversationEntry, 0)
	visited := make(map[conversation.EntryID]bool)
	for _, row := range rows {
		entry, err := row.toEntry()
		if err != nil {
			return nil, err
		}
		if visited[entry.ID] {
			return nil, fmt.Errorf("%w: cycle detected at entry %s", conversation.ErrEntryCorrupt, entry.ID)
		}
		visited[entry.ID] = true
		path = append(path, entry)
		if len(path) > maxEntries {
			return nil, fmt.Errorf("%w: path length exceeds max entries %d", conversation.ErrEntryCapacity, maxEntries)
		}
	}
	if result.RowsAffected == 0 || len(path) == 0 {
		return nil, fmt.Errorf("%w: entry %s not found in path", conversation.ErrEntryCorrupt, targetLeaf)
	}

	root := path[0]
	if root.ParentID != nil {
		return nil, fmt.Errorf("%w: entry %s not found in path", conversation.ErrEntryCorrupt, *root.ParentID)
	}
	if root.Depth != 0 {
		return nil, fmt.Errorf("%w: root entry %s has depth %d != 0", conversation.ErrEntryCorrupt, root.ID, root.Depth)
	}
	for i := 1; i < len(path); i++ {
		entry := path[i]
		previous := path[i-1]
		if entry.ParentID == nil || *entry.ParentID != previous.ID {
			return nil, fmt.Errorf("%w: path parent mismatch at entry %s", conversation.ErrEntryCorrupt, entry.ID)
		}
		if entry.Depth == 0 {
			return nil, fmt.Errorf("%w: non-root entry %s has depth 0", conversation.ErrEntryCorrupt, entry.ID)
		}
		expectedDepth := previous.Depth + 1
		if entry.Depth != expectedDepth {
			return nil, fmt.Errorf("%w: depth mismatch at entry %s: expected %d, got %d", conversation.ErrEntryCorrupt, entry.ID, expectedDepth, entry.Depth)
		}
	}
	if err := conversation.ValidatePathConsistency(path); err != nil {
		return nil, err
	}

	return path, nil
}

// Keep the recursive query and its maxEntries + 1 guard intact so that path
// hydration and validation retain their original order and capacity errors.
const entryPathSQL = `
		WITH RECURSIVE entry_path AS (
			SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at, 1 AS path_length
			FROM conversation_entry_nodes
			WHERE conversation_id = ? AND id = ?

			UNION ALL

			SELECT parent.id, parent.conversation_id, parent.parent_id, parent.kind, parent.payload_version,
				parent.payload_json, parent.append_index, parent.depth, parent.created_at, entry_path.path_length + 1
			FROM conversation_entry_nodes AS parent
			JOIN entry_path ON parent.conversation_id = entry_path.conversation_id
				AND parent.id = entry_path.parent_id
			WHERE entry_path.path_length <= ?
		)
		SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
		FROM entry_path
		ORDER BY path_length DESC
	`
