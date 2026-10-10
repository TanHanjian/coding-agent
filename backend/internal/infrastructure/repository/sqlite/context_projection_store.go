package sqlite

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

// ReadProjection binds history selection and its compatibility boundary to one
// committed database snapshot. No source selection happens outside this transaction.
func (r *AgentRunRepository) ReadProjection(ctx context.Context, query conversation.ProjectionQuery) (conversation.ProjectionSnapshot, error) {
	if strings.TrimSpace(query.ConversationID) == "" || strings.TrimSpace(query.UserMessageID) == "" {
		return conversation.ProjectionSnapshot{}, domainerr.ErrInvalidInput
	}
	var snapshot conversation.ProjectionSnapshot
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		if _, err := checkConversationExists(ctx, tx, query.ConversationID); err != nil {
			return err
		}
		head, exists, err := loadHeadTx(ctx, tx, query.ConversationID)
		if err != nil {
			return err
		}
		if head.Version < 0 || (head.ActiveLeafID != nil && head.Version == 0) {
			return corruptRun("invalid projection head")
		}
		user, err := takeChatMessageTx(ctx, tx.Where("conversation_id = ? AND id = ?", query.ConversationID, query.UserMessageID))
		if err != nil {
			return err
		}
		if user.Role != conversation.MessageRoleUser || user.Status != conversation.MessageStatusCompleted {
			return corruptRun("invalid projection user")
		}
		snapshot.Head = head.Clone()
		snapshot.Head.ConversationID = query.ConversationID
		if query.RunID == "" {
			var runs int64
			if err := tx.WithContext(ctx).Model(&runRow{}).Where("conversation_id = ?", query.ConversationID).Count(&runs).Error; err != nil {
				return err
			}
			if (exists && (head.ActiveLeafID != nil || head.Version != 0)) || runs != 0 {
				return corruptRun("legacy projection has tree-backed history")
			}
			snapshot.LegacyBeforeSequence = user.Sequence
			snapshot.Legacy, err = listCompletedHistoryBefore(ctx, tx, query.ConversationID, user.Sequence)
			return err
		}
		run, err := findRunTx(ctx, tx, tx.Where("id = ? AND conversation_id = ?", query.RunID, query.ConversationID))
		if err != nil {
			return err
		}
		if run.UserMessageID != user.ID {
			return corruptRun("projection run/user mismatch")
		}
		if (run.Status != agentrun.StatusPending && run.Status != agentrun.StatusRunning) || run.LastEntryID != run.UserEntryID {
			return domainerr.ErrConflict
		}
		token, err := run.ActiveHeadToken()
		if err != nil {
			return err
		}
		if _, err := r.activeHeadTx(ctx, tx, run, token); err != nil {
			return err
		}
		path, err := readPathTx(ctx, tx, query.ConversationID, run.UserEntryID, r.tree.maxPathEntries)
		if err != nil {
			return err
		}
		if !sameEntryID(path[len(path)-1].ParentID, run.BaseEntryID) {
			return corruptRun("projection base mismatch")
		}
		path = path[:len(path)-1]
		if !conversation.IsClosedBranchPoint(path) {
			return corruptRun("projection history contains pending tool group")
		}

		// Sequence is only a legacy coverage boundary, never a tree parent.
		var boundaryRow runRow
		if err := tx.WithContext(ctx).Table("agent_runs AS r").Select("r.*").
			Joins("JOIN messages AS m ON m.id = r.user_message_id AND m.conversation_id = r.conversation_id").
			Where("r.conversation_id = ?", query.ConversationID).Order("m.sequence ASC").Take(&boundaryRow).Error; err != nil {
			return err
		}
		boundary, err := boundaryRow.toRun()
		if err != nil {
			return err
		}
		boundaryUser, _, err := r.validateRunReferencesTx(ctx, tx, boundary)
		if err != nil {
			return err
		}
		if boundary.BaseEntryID != nil || boundaryUser.Sequence > user.Sequence {
			return corruptRun("missing projection legacy boundary")
		}
		rootID := run.UserEntryID
		if len(path) > 0 {
			rootID = path[0].ID
		}
		if rootID != boundary.UserEntryID {
			return corruptRun("projection root does not match legacy boundary")
		}
		var uncovered int64
		if err := tx.WithContext(ctx).Table("messages AS m").Where("m.conversation_id = ? AND m.sequence >= ? AND m.sequence < ? AND m.status = ?", query.ConversationID, boundaryUser.Sequence, user.Sequence, conversation.MessageStatusCompleted).
			Where("NOT EXISTS (SELECT 1 FROM agent_runs AS r WHERE r.conversation_id = m.conversation_id AND (r.user_message_id = m.id OR r.assistant_message_id = m.id))").Count(&uncovered).Error; err != nil {
			return err
		}
		if uncovered != 0 {
			return corruptRun("uncovered legacy messages after tree boundary")
		}
		snapshot.LegacyBeforeSequence = boundaryUser.Sequence
		snapshot.Legacy, err = listCompletedHistoryBefore(ctx, tx, query.ConversationID, boundaryUser.Sequence)
		if err != nil {
			return err
		}
		snapshot.Entries = make([]conversation.ConversationEntry, len(path))
		for i, entry := range path {
			snapshot.Entries[i] = entry.Clone()
		}
		return nil
	})
	if err != nil {
		return conversation.ProjectionSnapshot{}, err
	}
	return snapshot, nil
}

var _ conversation.ProjectionReader = (*AgentRunRepository)(nil)
