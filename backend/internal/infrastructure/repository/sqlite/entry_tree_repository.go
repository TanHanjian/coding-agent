package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

type EntryTreeOptions struct {
	NewID           func() (conversation.EntryID, error)
	Now             func() time.Time
	MaxPayloadBytes int
	MaxPathEntries  int
}

type EntryTreeRepository struct {
	db              *storage.DB
	newID           func() (conversation.EntryID, error)
	now             func() time.Time
	maxPayloadBytes int
	maxPathEntries  int
}

var _ conversation.EntryTree = (*EntryTreeRepository)(nil)

func NewEntryTreeRepository(db *storage.DB, options EntryTreeOptions) (*EntryTreeRepository, error) {
	if db == nil {
		return nil, fmt.Errorf("entry tree repository: storage.DB is nil")
	}
	newID := options.NewID
	if newID == nil {
		newID = conversation.NewEntryID
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	maxPayloadBytes := options.MaxPayloadBytes
	if maxPayloadBytes <= 0 {
		maxPayloadBytes = 1024 * 1024 // 1 MiB default
	}
	maxPathEntries := options.MaxPathEntries
	if maxPathEntries <= 0 {
		maxPathEntries = 10000 // 10000 entries default
	}

	return &EntryTreeRepository{
		db:              db,
		newID:           newID,
		now:             now,
		maxPayloadBytes: maxPayloadBytes,
		maxPathEntries:  maxPathEntries,
	}, nil
}

func (r *EntryTreeRepository) GetHead(ctx context.Context, conversationID string) (conversation.EntryHead, error) {
	var head conversation.EntryHead
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		convUpdatedAt, err := checkConversationExists(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		loadedHead, exists, err := loadHeadTx(ctx, tx, conversationID)
		if err != nil {
			return err
		}
		if exists {
			head = loadedHead.Clone()
			return nil
		}
		head = conversation.EntryHead{
			ConversationID: conversationID,
			ActiveLeafID:   nil,
			Version:        0,
			UpdatedAt:      convUpdatedAt,
		}
		return nil
	})
	if err != nil {
		return conversation.EntryHead{}, err
	}
	return head, nil
}

func (r *EntryTreeRepository) Get(ctx context.Context, ref conversation.EntryRef) (conversation.ConversationEntry, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
		FROM conversation_entry_nodes
		WHERE conversation_id = ? AND id = ?
	`, ref.ConversationID, string(ref.ID))

	entry, err := scanAndHydrateEntry(row)
	if err == sql.ErrNoRows {
		return conversation.ConversationEntry{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.ConversationEntry{}, err
	}
	return entry, nil
}

func (r *EntryTreeRepository) ReadPath(ctx context.Context, query conversation.PathQuery) (conversation.EntryPath, error) {
	if query.UseActiveLeaf && query.LeafID != nil {
		return conversation.EntryPath{}, fmt.Errorf("%w: LeafID must be nil when UseActiveLeaf is true", domainerr.ErrInvalidInput)
	}

	var result conversation.EntryPath
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		convUpdatedAt, err := checkConversationExists(ctx, tx, query.ConversationID)
		if err != nil {
			return err
		}

		head, exists, err := loadHeadTx(ctx, tx, query.ConversationID)
		if err != nil {
			return err
		}
		if !exists {
			head = conversation.EntryHead{
				ConversationID: query.ConversationID,
				ActiveLeafID:   nil,
				Version:        0,
				UpdatedAt:      convUpdatedAt,
			}
		}

		var targetLeaf *conversation.EntryID
		if query.UseActiveLeaf {
			targetLeaf = head.ActiveLeafID
		} else {
			targetLeaf = query.LeafID
		}

		if targetLeaf == nil {
			result = conversation.EntryPath{
				Head:    head.Clone(),
				LeafID:  nil,
				Entries: []conversation.ConversationEntry{},
			}
			return nil
		}

		entries, err := readPathTx(ctx, tx, query.ConversationID, *targetLeaf, r.maxPathEntries)
		if err != nil {
			return err
		}

		result = conversation.EntryPath{
			Head:    head.Clone(),
			LeafID:  targetLeaf,
			Entries: entries,
		}
		return nil
	})
	if err != nil {
		return conversation.EntryPath{}, err
	}
	return result, nil
}

func (r *EntryTreeRepository) ListChildren(ctx context.Context, query conversation.ChildrenQuery) (conversation.ChildrenPage, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}

	var page conversation.ChildrenPage
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := checkConversationExists(ctx, tx, query.ConversationID); err != nil {
			return err
		}

		var rows *sql.Rows
		var err error
		fetchLimit := limit + 1

		if query.ParentID == nil {
			rows, err = tx.QueryContext(ctx, `
				SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
				FROM conversation_entry_nodes
				WHERE conversation_id = ? AND parent_id IS NULL AND append_index > ?
				ORDER BY append_index ASC
				LIMIT ?
			`, query.ConversationID, query.AfterIndex, fetchLimit)
		} else {
			rows, err = tx.QueryContext(ctx, `
				SELECT id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
				FROM conversation_entry_nodes
				WHERE conversation_id = ? AND parent_id = ? AND append_index > ?
				ORDER BY append_index ASC
				LIMIT ?
			`, query.ConversationID, string(*query.ParentID), query.AfterIndex, fetchLimit)
		}
		if err != nil {
			return err
		}
		defer rows.Close()

		var entries []conversation.ConversationEntry
		for rows.Next() {
			entry, err := scanAndHydrateEntry(rows)
			if err != nil {
				return err
			}
			entries = append(entries, entry)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(entries) > limit {
			nextIdx := entries[limit-1].AppendIndex
			page = conversation.ChildrenPage{
				Entries:   entries[:limit],
				NextIndex: &nextIdx,
			}
		} else {
			page = conversation.ChildrenPage{
				Entries:   entries,
				NextIndex: nil,
			}
		}
		return nil
	})
	if err != nil {
		return conversation.ChildrenPage{}, err
	}
	return page, nil
}

func (r *EntryTreeRepository) Append(ctx context.Context, input conversation.AppendEntryInput) (conversation.AppendEntryResult, error) {
	msgCopy := input.Message.Clone()
	encodedJSON, err := EncodeEntryMessage(msgCopy)
	if err != nil {
		return conversation.AppendEntryResult{}, err
	}
	if len(encodedJSON) > r.maxPayloadBytes {
		return conversation.AppendEntryResult{}, conversation.NewEntryError(
			conversation.EntryErrCodeCapacity,
			"",
			fmt.Sprintf("payload size %d exceeds max bytes %d", len(encodedJSON), r.maxPayloadBytes),
			conversation.ErrEntryCapacity,
		)
	}

	var result conversation.AppendEntryResult
	err = r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := checkConversationExists(ctx, tx, input.ConversationID); err != nil {
			return err
		}

		now := r.now().UTC()
		head, err := lazyInitHeadTx(ctx, tx, input.ConversationID, now)
		if err != nil {
			return err
		}

		// Verify expected head
		if head.Version != input.ExpectedHead.Version {
			return domainerr.ErrConflict
		}
		if (head.ActiveLeafID == nil && input.ExpectedHead.ActiveLeafID != nil) ||
			(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID == nil) ||
			(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID != nil && *head.ActiveLeafID != *input.ExpectedHead.ActiveLeafID) {
			return domainerr.ErrConflict
		}

		var parentPath []conversation.ConversationEntry
		var depth int64
		if head.ActiveLeafID != nil {
			path, err := readPathTx(ctx, tx, input.ConversationID, *head.ActiveLeafID, r.maxPathEntries)
			if err != nil {
				return err
			}
			parentPath = path
			depth = path[len(path)-1].Depth + 1
		}

		if depth+1 > int64(r.maxPathEntries) {
			return conversation.NewEntryError(
				conversation.EntryErrCodeCapacity,
				"",
				fmt.Sprintf("path depth %d exceeds max entries %d", depth+1, r.maxPathEntries),
				conversation.ErrEntryCapacity,
			)
		}

		if err := conversation.ValidateToolContinuation(parentPath, msgCopy); err != nil {
			return err
		}

		var maxIndex sql.NullInt64
		if err := tx.QueryRowContext(ctx, "SELECT MAX(append_index) FROM conversation_entry_nodes WHERE conversation_id = ?", input.ConversationID).Scan(&maxIndex); err != nil {
			return err
		}
		var appendIndex int64 = 1
		if maxIndex.Valid {
			appendIndex = maxIndex.Int64 + 1
		}

		newID, err := r.newID()
		if err != nil {
			return err
		}

		var parentIDVal any
		if head.ActiveLeafID != nil {
			parentIDVal = string(*head.ActiveLeafID)
		}

		nowStr := now.Format(time.RFC3339Nano)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO conversation_entry_nodes (
				id, conversation_id, parent_id, kind, payload_version, payload_json, append_index, depth, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, string(newID), input.ConversationID, parentIDVal, string(conversation.EntryKindMessage), conversation.EntryPayloadV1, string(encodedJSON), appendIndex, depth, nowStr)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx, `
			UPDATE conversation_entry_heads
			SET active_leaf_id = ?, version = version + 1, updated_at = ?
			WHERE conversation_id = ?
			  AND version = ?
			  AND active_leaf_id IS ?;
		`, string(newID), nowStr, input.ConversationID, input.ExpectedHead.Version, parentIDVal)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return domainerr.ErrConflict
		}

		newHead := conversation.EntryHead{
			ConversationID: input.ConversationID,
			ActiveLeafID:   &newID,
			Version:        head.Version + 1,
			UpdatedAt:      now,
		}

		var pidPtr *conversation.EntryID
		if head.ActiveLeafID != nil {
			pid := *head.ActiveLeafID
			pidPtr = &pid
		}

		newEntry := conversation.ConversationEntry{
			ID:             newID,
			ConversationID: input.ConversationID,
			ParentID:       pidPtr,
			Kind:           conversation.EntryKindMessage,
			PayloadVersion: conversation.EntryPayloadV1,
			Message:        msgCopy,
			AppendIndex:    appendIndex,
			Depth:          depth,
			CreatedAt:      now,
		}

		result = conversation.AppendEntryResult{
			Entry: newEntry,
			Head:  newHead,
		}
		return nil
	})

	if err != nil {
		return conversation.AppendEntryResult{}, err
	}
	return result, nil
}

func (r *EntryTreeRepository) MoveHead(ctx context.Context, input conversation.MoveHeadInput) (conversation.EntryHead, error) {
	var newHead conversation.EntryHead
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := checkConversationExists(ctx, tx, input.ConversationID); err != nil {
			return err
		}

		now := r.now().UTC()
		head, err := lazyInitHeadTx(ctx, tx, input.ConversationID, now)
		if err != nil {
			return err
		}

		if head.Version != input.ExpectedHead.Version {
			return domainerr.ErrConflict
		}
		if (head.ActiveLeafID == nil && input.ExpectedHead.ActiveLeafID != nil) ||
			(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID == nil) ||
			(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID != nil && *head.ActiveLeafID != *input.ExpectedHead.ActiveLeafID) {
			return domainerr.ErrConflict
		}

		if input.TargetID != nil {
			targetPath, err := readPathTx(ctx, tx, input.ConversationID, *input.TargetID, r.maxPathEntries)
			if err != nil {
				return err
			}
			if !conversation.IsClosedBranchPoint(targetPath) {
				return fmt.Errorf("%w: target entry %s is in an unclosed tool group", domainerr.ErrInvalidInput, *input.TargetID)
			}
		}

		var oldLeafVal any
		if head.ActiveLeafID != nil {
			oldLeafVal = string(*head.ActiveLeafID)
		}
		var newLeafVal any
		if input.TargetID != nil {
			newLeafVal = string(*input.TargetID)
		}

		nowStr := now.Format(time.RFC3339Nano)
		res, err := tx.ExecContext(ctx, `
			UPDATE conversation_entry_heads
			SET active_leaf_id = ?, version = version + 1, updated_at = ?
			WHERE conversation_id = ?
			  AND version = ?
			  AND active_leaf_id IS ?;
		`, newLeafVal, nowStr, input.ConversationID, input.ExpectedHead.Version, oldLeafVal)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return domainerr.ErrConflict
		}

		newHead = conversation.EntryHead{
			ConversationID: input.ConversationID,
			ActiveLeafID:   input.TargetID,
			Version:        head.Version + 1,
			UpdatedAt:      now,
		}
		return nil
	})

	if err != nil {
		return conversation.EntryHead{}, err
	}
	return newHead, nil
}
