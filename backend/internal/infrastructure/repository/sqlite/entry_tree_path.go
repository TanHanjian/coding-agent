package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

func checkConversationExists(ctx context.Context, tx *sql.Tx, conversationID string) (time.Time, error) {
	var updatedAtStr string
	err := tx.QueryRowContext(ctx, "SELECT updated_at FROM conversations WHERE id = ?", conversationID).Scan(&updatedAtStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, domainerr.ErrNotFound
		}
		return time.Time{}, err
	}
	t, _ := parseRFC3339(updatedAtStr)
	return t, nil
}

func loadHeadTx(ctx context.Context, tx *sql.Tx, conversationID string) (conversation.EntryHead, bool, error) {
	var activeLeafID sql.NullString
	var version int64
	var updatedAtStr string

	err := tx.QueryRowContext(ctx,
		"SELECT active_leaf_id, version, updated_at FROM conversation_entry_heads WHERE conversation_id = ?",
		conversationID,
	).Scan(&activeLeafID, &version, &updatedAtStr)

	if err == nil {
		updatedAt, _ := parseRFC3339(updatedAtStr)
		var leaf *conversation.EntryID
		if activeLeafID.Valid {
			eid := conversation.EntryID(activeLeafID.String)
			leaf = &eid
		}
		return conversation.EntryHead{
			ConversationID: conversationID,
			ActiveLeafID:   leaf,
			Version:        version,
			UpdatedAt:      updatedAt,
		}, true, nil
	}

	if err != sql.ErrNoRows {
		return conversation.EntryHead{}, false, err
	}

	// No head found in table: check if any nodes exist
	var count int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(1) FROM conversation_entry_nodes WHERE conversation_id = ?",
		conversationID,
	).Scan(&count); err != nil {
		return conversation.EntryHead{}, false, err
	}

	if count > 0 {
		return conversation.EntryHead{}, false, fmt.Errorf("%w: missing head for non-empty conversation tree", conversation.ErrEntryCorrupt)
	}

	return conversation.EntryHead{}, false, nil
}

func lazyInitHeadTx(ctx context.Context, tx *sql.Tx, conversationID string, now time.Time) (conversation.EntryHead, error) {
	head, exists, err := loadHeadTx(ctx, tx, conversationID)
	if err != nil {
		return conversation.EntryHead{}, err
	}
	if exists {
		return head, nil
	}

	nowStr := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO conversation_entry_heads (conversation_id, active_leaf_id, version, updated_at) VALUES (?, NULL, 0, ?)",
		conversationID, nowStr,
	); err != nil {
		return conversation.EntryHead{}, err
	}

	return conversation.EntryHead{
		ConversationID: conversationID,
		ActiveLeafID:   nil,
		Version:        0,
		UpdatedAt:      now.UTC(),
	}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAndHydrateEntry(scanner rowScanner) (conversation.ConversationEntry, error) {
	var id, convID, kind, payloadJSON, createdAtStr string
	var parentID sql.NullString
	var payloadVersion int
	var appendIndex, depth int64

	if err := scanner.Scan(
		&id, &convID, &parentID, &kind, &payloadVersion, &payloadJSON, &appendIndex, &depth, &createdAtStr,
	); err != nil {
		return conversation.ConversationEntry{}, err
	}

	msg, err := DecodeEntryMessage(payloadVersion, []byte(payloadJSON))
	if err != nil {
		return conversation.ConversationEntry{}, err
	}

	createdAt, err := parseRFC3339(createdAtStr)
	if err != nil {
		return conversation.ConversationEntry{}, fmt.Errorf("%w: corrupt created_at for entry %s: %v", conversation.ErrEntryCorrupt, id, err)
	}

	var pidPtr *conversation.EntryID
	if parentID.Valid {
		pid := conversation.EntryID(parentID.String)
		pidPtr = &pid
	}

	return conversation.ConversationEntry{
		ID:             conversation.EntryID(id),
		ConversationID: convID,
		ParentID:       pidPtr,
		Kind:           conversation.EntryKind(kind),
		PayloadVersion: payloadVersion,
		Message:        msg,
		AppendIndex:    appendIndex,
		Depth:          depth,
		CreatedAt:      createdAt,
	}, nil
}

func readPathTx(ctx context.Context, tx *sql.Tx, conversationID string, targetLeaf conversation.EntryID, maxEntries int) ([]conversation.ConversationEntry, error) {
	currentID := targetLeaf
	visited := make(map[conversation.EntryID]bool)
	var pathReversed []conversation.ConversationEntry
	var expectedDepth *int64

	for {
		if visited[currentID] {
			return nil, fmt.Errorf("%w: cycle detected at entry %s", conversation.ErrEntryCorrupt, currentID)
		}
		visited[currentID] = true

		row := tx.QueryRowContext(ctx, `
			SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
			FROM conversation_entry_nodes
			WHERE conversation_id = ? AND id = ?
		`, conversationID, string(currentID))

		entry, err := scanAndHydrateEntry(row)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("%w: entry %s not found in path", conversation.ErrEntryCorrupt, currentID)
			}
			return nil, err
		}

		if expectedDepth != nil && entry.Depth != *expectedDepth {
			return nil, fmt.Errorf("%w: depth mismatch at entry %s: expected %d, got %d", conversation.ErrEntryCorrupt, entry.ID, *expectedDepth, entry.Depth)
		}

		pathReversed = append(pathReversed, entry)

		if len(pathReversed) > maxEntries {
			return nil, fmt.Errorf("%w: path length exceeds max entries %d", conversation.ErrEntryCapacity, maxEntries)
		}

		if entry.ParentID == nil {
			if entry.Depth != 0 {
				return nil, fmt.Errorf("%w: root entry %s has depth %d != 0", conversation.ErrEntryCorrupt, entry.ID, entry.Depth)
			}
			break
		}

		if entry.Depth == 0 {
			return nil, fmt.Errorf("%w: non-root entry %s has depth 0", conversation.ErrEntryCorrupt, entry.ID)
		}

		parentExpDepth := entry.Depth - 1
		expectedDepth = &parentExpDepth
		currentID = *entry.ParentID
	}

	n := len(pathReversed)
	path := make([]conversation.ConversationEntry, n)
	for i, e := range pathReversed {
		path[n-1-i] = e
	}

	if err := conversation.ValidatePathConsistency(path); err != nil {
		return nil, err
	}

	return path, nil
}
