package sqlite

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

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
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
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
	var row entryNodeRow
	result := r.db.ORM(ctx).
		Where("conversation_id = ? AND id = ?", ref.ConversationID, string(ref.ID)).
		Take(&row)
	if result.Error == gorm.ErrRecordNotFound {
		return conversation.ConversationEntry{}, domainerr.ErrNotFound
	}
	if result.Error != nil {
		return conversation.ConversationEntry{}, result.Error
	}
	return row.toEntry()
}

func (r *EntryTreeRepository) ReadPath(ctx context.Context, query conversation.PathQuery) (conversation.EntryPath, error) {
	if query.UseActiveLeaf && query.LeafID != nil {
		return conversation.EntryPath{}, fmt.Errorf("%w: LeafID must be nil when UseActiveLeaf is true", domainerr.ErrInvalidInput)
	}

	var result conversation.EntryPath
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
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
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if _, err := checkConversationExists(ctx, tx, query.ConversationID); err != nil {
			return err
		}

		var rows []entryNodeRow
		fetchLimit := limit + 1
		children := tx.WithContext(ctx).
			Where("conversation_id = ? AND append_index > ?", query.ConversationID, query.AfterIndex)
		if query.ParentID == nil {
			children = children.Where("parent_id IS NULL")
		} else {
			children = children.Where("parent_id = ?", string(*query.ParentID))
		}
		if err := children.Order("append_index ASC").Limit(fetchLimit).Find(&rows).Error; err != nil {
			return err
		}

		var entries []conversation.ConversationEntry
		for _, row := range rows {
			entry, err := row.toEntry()
			if err != nil {
				return err
			}
			entries = append(entries, entry)
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
	msgCopy, _, err := r.prepareAppendMessage(input.Message)
	if err != nil {
		return conversation.AppendEntryResult{}, err
	}
	// Keep the pre-transaction snapshot; appendTx validates every write entry.
	input.Message = msgCopy
	var result conversation.AppendEntryResult
	err = r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.appendTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return conversation.AppendEntryResult{}, err
	}
	return result, nil
}

func (r *EntryTreeRepository) prepareAppendMessage(message conversation.EntryMessage) (conversation.EntryMessage, []byte, error) {
	msgCopy := message.Clone()
	encodedJSON, err := EncodeEntryMessage(msgCopy)
	if err != nil {
		return conversation.EntryMessage{}, nil, err
	}
	if len(encodedJSON) > r.maxPayloadBytes {
		return conversation.EntryMessage{}, nil, conversation.NewEntryError(
			conversation.EntryErrCodeCapacity,
			"",
			fmt.Sprintf("payload size %d exceeds max bytes %d", len(encodedJSON), r.maxPayloadBytes),
			conversation.ErrEntryCapacity,
		)
	}
	return msgCopy, encodedJSON, nil
}

// appendTx uses only the caller's transaction. Its result is provisional until
// the owner commits; any error must abort the entire transaction.
type appendSnapshot struct {
	head       conversation.EntryHead
	parentPath []conversation.ConversationEntry
}

func (r *EntryTreeRepository) appendTx(ctx context.Context, tx *gorm.DB, input conversation.AppendEntryInput) (conversation.AppendEntryResult, error) {
	return r.appendTxWithSnapshot(ctx, tx, input, nil)
}

func (r *EntryTreeRepository) appendTxWithSnapshot(ctx context.Context, tx *gorm.DB, input conversation.AppendEntryInput, snapshot *appendSnapshot) (conversation.AppendEntryResult, error) {
	msgCopy, encodedJSON, err := r.prepareAppendMessage(input.Message)
	if err != nil {
		return conversation.AppendEntryResult{}, err
	}

	var head conversation.EntryHead
	var parentPath []conversation.ConversationEntry
	now := r.now().UTC()
	if snapshot == nil {
		if _, err := checkConversationExists(ctx, tx, input.ConversationID); err != nil {
			return conversation.AppendEntryResult{}, err
		}

		head, err = lazyInitHeadTx(ctx, tx, input.ConversationID, now)
		if err != nil {
			return conversation.AppendEntryResult{}, err
		}
		if head.ActiveLeafID != nil {
			path, err := readPathTx(ctx, tx, input.ConversationID, *head.ActiveLeafID, r.maxPathEntries)
			if err != nil {
				return conversation.AppendEntryResult{}, err
			}
			parentPath = path
		}
	} else {
		head = snapshot.head
		parentPath = snapshot.parentPath
	}

	// Verify expected head. The update below repeats this check at write time.
	if head.Version != input.ExpectedHead.Version {
		return conversation.AppendEntryResult{}, domainerr.ErrConflict
	}
	if (head.ActiveLeafID == nil && input.ExpectedHead.ActiveLeafID != nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID == nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID != nil && *head.ActiveLeafID != *input.ExpectedHead.ActiveLeafID) {
		return conversation.AppendEntryResult{}, domainerr.ErrConflict
	}

	var depth int64
	if head.ActiveLeafID != nil {
		if len(parentPath) == 0 || parentPath[len(parentPath)-1].ID != *head.ActiveLeafID {
			return conversation.AppendEntryResult{}, fmt.Errorf("%w: append snapshot does not match head", conversation.ErrEntryCorrupt)
		}
		depth = parentPath[len(parentPath)-1].Depth + 1
	}

	if depth+1 > int64(r.maxPathEntries) {
		return conversation.AppendEntryResult{}, conversation.NewEntryError(
			conversation.EntryErrCodeCapacity,
			"",
			fmt.Sprintf("path depth %d exceeds max entries %d", depth+1, r.maxPathEntries),
			conversation.ErrEntryCapacity,
		)
	}

	if err := conversation.ValidateToolContinuation(parentPath, msgCopy); err != nil {
		return conversation.AppendEntryResult{}, err
	}

	var maxIndex entryAppendIndexRow
	if err := tx.WithContext(ctx).Model(&entryNodeRow{}).
		Select("MAX(append_index) AS max_index").
		Where("conversation_id = ?", input.ConversationID).Scan(&maxIndex).Error; err != nil {
		return conversation.AppendEntryResult{}, err
	}
	var appendIndex int64 = 1
	if maxIndex.MaxIndex != nil {
		appendIndex = *maxIndex.MaxIndex + 1
	}

	newID, err := r.newID()
	if err != nil {
		return conversation.AppendEntryResult{}, err
	}

	var parentID *string
	if head.ActiveLeafID != nil {
		id := string(*head.ActiveLeafID)
		parentID = &id
	}

	nowStr := now.Format(time.RFC3339Nano)
	row := entryNodeRow{
		ID:             string(newID),
		ConversationID: input.ConversationID,
		ParentID:       parentID,
		Kind:           string(conversation.EntryKindMessage),
		PayloadVersion: conversation.EntryPayloadV1,
		PayloadJSON:    string(encodedJSON),
		AppendIndex:    appendIndex,
		Depth:          depth,
		CreatedAt:      nowStr,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return conversation.AppendEntryResult{}, err
	}

	res := tx.WithContext(ctx).Model(&entryHeadRow{}).
		Where("conversation_id = ? AND version = ? AND active_leaf_id IS ?", input.ConversationID, input.ExpectedHead.Version, parentID).
		Updates(map[string]any{
			"active_leaf_id": string(newID),
			"version":        gorm.Expr("version + 1"),
			"updated_at":     nowStr,
		})
	if res.Error != nil {
		return conversation.AppendEntryResult{}, res.Error
	}
	if res.RowsAffected != 1 {
		return conversation.AppendEntryResult{}, domainerr.ErrConflict
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
		Message:        &msgCopy,
		AppendIndex:    appendIndex,
		Depth:          depth,
		CreatedAt:      now,
	}

	return conversation.AppendEntryResult{
		Entry: newEntry,
		Head:  newHead,
	}, nil
}

func (r *EntryTreeRepository) AppendCompaction(ctx context.Context, input conversation.AppendCompactionInput) (conversation.AppendCompactionResult, error) {
	encodedJSON, err := EncodeEntryCompaction(input.Compaction)
	if err != nil {
		return conversation.AppendCompactionResult{}, err
	}
	if len(encodedJSON) > r.maxPayloadBytes {
		return conversation.AppendCompactionResult{}, conversation.NewEntryError(
			conversation.EntryErrCodeCapacity,
			"",
			fmt.Sprintf("compaction payload size %d exceeds max bytes %d", len(encodedJSON), r.maxPayloadBytes),
			conversation.ErrEntryCapacity,
		)
	}

	var result conversation.AppendCompactionResult
	err = r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.appendCompactionTx(ctx, tx, input, encodedJSON)
		return err
	})
	if err != nil {
		return conversation.AppendCompactionResult{}, err
	}
	return result, nil
}

func (r *EntryTreeRepository) appendCompactionTx(ctx context.Context, tx *gorm.DB, input conversation.AppendCompactionInput, encodedJSON []byte) (conversation.AppendCompactionResult, error) {
	if _, err := checkConversationExists(ctx, tx, input.ConversationID); err != nil {
		return conversation.AppendCompactionResult{}, err
	}

	now := r.now().UTC()
	head, err := lazyInitHeadTx(ctx, tx, input.ConversationID, now)
	if err != nil {
		return conversation.AppendCompactionResult{}, err
	}

	// Verify expected head (CAS)
	if head.Version != input.ExpectedHead.Version {
		return conversation.AppendCompactionResult{}, domainerr.ErrConflict
	}
	if (head.ActiveLeafID == nil && input.ExpectedHead.ActiveLeafID != nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID == nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID != nil && *head.ActiveLeafID != *input.ExpectedHead.ActiveLeafID) {
		return conversation.AppendCompactionResult{}, domainerr.ErrConflict
	}

	// Get next append index
	var maxIndex entryAppendIndexRow
	if err := tx.WithContext(ctx).Model(&entryNodeRow{}).
		Select("MAX(append_index) AS max_index").
		Where("conversation_id = ?", input.ConversationID).Scan(&maxIndex).Error; err != nil {
		return conversation.AppendCompactionResult{}, err
	}
	var appendIndex int64 = 1
	if maxIndex.MaxIndex != nil {
		appendIndex = *maxIndex.MaxIndex + 1
	}

	newID, err := r.newID()
	if err != nil {
		return conversation.AppendCompactionResult{}, err
	}

	var parentID *string
	if head.ActiveLeafID != nil {
		id := string(*head.ActiveLeafID)
		parentID = &id
	}

	nowStr := now.Format(time.RFC3339Nano)
	row := entryNodeRow{
		ID:             string(newID),
		ConversationID: input.ConversationID,
		ParentID:       parentID,
		Kind:           string(conversation.EntryKindCompaction),
		PayloadVersion: 1,
		PayloadJSON:    string(encodedJSON),
		AppendIndex:    appendIndex,
		Depth:          0, // Compaction entries don't participate in depth calculation
		CreatedAt:      nowStr,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return conversation.AppendCompactionResult{}, err
	}

	// Update head with CAS check
	res := tx.WithContext(ctx).Model(&entryHeadRow{}).
		Where("conversation_id = ? AND version = ? AND active_leaf_id IS ?", input.ConversationID, input.ExpectedHead.Version, parentID).
		Updates(map[string]any{
			"active_leaf_id": string(newID),
			"version":        gorm.Expr("version + 1"),
			"updated_at":     nowStr,
		})
	if res.Error != nil {
		return conversation.AppendCompactionResult{}, res.Error
	}
	if res.RowsAffected != 1 {
		return conversation.AppendCompactionResult{}, domainerr.ErrConflict
	}

	newHead := conversation.EntryHead{
		ConversationID: input.ConversationID,
		ActiveLeafID:   &newID,
		Version:        head.Version + 1,
		UpdatedAt:      now,
	}

	compactionCopy := input.Compaction
	newEntry := conversation.ConversationEntry{
		ID:             newID,
		ConversationID: input.ConversationID,
		ParentID:       head.ActiveLeafID,
		Kind:           conversation.EntryKindCompaction,
		PayloadVersion: 1,
		Message:        nil,
		Compaction:     &compactionCopy,
		AppendIndex:    appendIndex,
		Depth:          0,
		CreatedAt:      now,
	}

	return conversation.AppendCompactionResult{
		Entry: newEntry,
		Head:  newHead,
	}, nil
}

func (r *EntryTreeRepository) MoveHead(ctx context.Context, input conversation.MoveHeadInput) (conversation.EntryHead, error) {
	var newHead conversation.EntryHead
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		newHead, err = r.moveHeadTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return conversation.EntryHead{}, err
	}
	return newHead, nil
}

// moveHeadTx follows the same commit/abort ownership contract as appendTx.
func (r *EntryTreeRepository) moveHeadTx(ctx context.Context, tx *gorm.DB, input conversation.MoveHeadInput) (conversation.EntryHead, error) {
	if _, err := checkConversationExists(ctx, tx, input.ConversationID); err != nil {
		return conversation.EntryHead{}, err
	}

	now := r.now().UTC()
	head, err := lazyInitHeadTx(ctx, tx, input.ConversationID, now)
	if err != nil {
		return conversation.EntryHead{}, err
	}

	if head.Version != input.ExpectedHead.Version {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}
	if (head.ActiveLeafID == nil && input.ExpectedHead.ActiveLeafID != nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID == nil) ||
		(head.ActiveLeafID != nil && input.ExpectedHead.ActiveLeafID != nil && *head.ActiveLeafID != *input.ExpectedHead.ActiveLeafID) {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}

	if input.TargetID != nil {
		targetPath, err := readPathTx(ctx, tx, input.ConversationID, *input.TargetID, r.maxPathEntries)
		if err != nil {
			return conversation.EntryHead{}, err
		}
		if !conversation.IsClosedBranchPoint(targetPath) {
			return conversation.EntryHead{}, fmt.Errorf("%w: target entry %s is in an unclosed tool group", domainerr.ErrInvalidInput, *input.TargetID)
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
	res := tx.WithContext(ctx).Model(&entryHeadRow{}).
		Where("conversation_id = ? AND version = ? AND active_leaf_id IS ?", input.ConversationID, input.ExpectedHead.Version, oldLeafVal).
		Updates(map[string]any{
			"active_leaf_id": newLeafVal,
			"version":        gorm.Expr("version + 1"),
			"updated_at":     nowStr,
		})
	if res.Error != nil {
		return conversation.EntryHead{}, res.Error
	}
	if res.RowsAffected != 1 {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}

	return conversation.EntryHead{
		ConversationID: input.ConversationID,
		ActiveLeafID:   input.TargetID,
		Version:        head.Version + 1,
		UpdatedAt:      now,
	}, nil
}
