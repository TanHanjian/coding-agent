package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// AgentRunRepository owns atomic Run, Entry Tree and legacy Message writes.
// The legacy ChatTurnRepository remains a separate, unchanged production path.
type AgentRunRepository struct {
	db    *storage.DB
	tree  *EntryTreeRepository
	turns *ChatTurnRepository
}

func NewAgentRunRepository(db *storage.DB) (*AgentRunRepository, error) {
	tree, err := NewEntryTreeRepository(db, EntryTreeOptions{})
	if err != nil {
		return nil, err
	}
	return &AgentRunRepository{db: db, tree: tree, turns: NewChatTurnRepository(db)}, nil
}

// BeginTurn returns only committed results. A retry resolves the original Run
// or legacy pair before checking busy; legacy-only turns are never backfilled.
func (r *AgentRunRepository) BeginTurn(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	if err := validateBeginTurnInput(input); err != nil {
		return chat.BeginTurnResult{}, err
	}
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.ClientMessageID = strings.TrimSpace(input.ClientMessageID)
	var result chat.BeginTurnResult
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.beginTurnTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	return result, nil
}

func (r *AgentRunRepository) beginTurnTx(ctx context.Context, tx *gorm.DB, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	currentConversation, err := loadConversationForTurn(ctx, tx, input.ConversationID)
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	run, err := findRunByClientIDTx(ctx, tx, input.ConversationID, input.ClientMessageID)
	if err == nil {
		user, assistant, err := r.validateRunReferencesTx(ctx, tx, run)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		history, err := listCompletedHistoryBefore(ctx, tx, input.ConversationID, user.Sequence)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		copy := run.Clone()
		return chat.BeginTurnResult{Conversation: currentConversation, History: history,
			UserMessage: user, AssistantMessage: assistant, Run: &copy, Reused: true}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return chat.BeginTurnResult{}, err
	}

	user, err := findUserMessageByClientID(ctx, tx, input.ConversationID, input.ClientMessageID)
	if err == nil {
		assistant, err := findPairedAssistantMessage(ctx, tx, user)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		if user.Status != conversation.MessageStatusCompleted || assistant.ClientMessageID != "" || !validAssistantTurnStatus(assistant.Status) {
			return chat.BeginTurnResult{}, corruptRun("invalid legacy message pair")
		}
		// A Run pointing at this pair under a different client key is corruption,
		// not a legacy-only retry.
		var linkedCount int64
		if err := tx.WithContext(ctx).Model(&runRow{}).
			Where("user_message_id = ? OR assistant_message_id = ?", user.ID, assistant.ID).
			Count(&linkedCount).Error; err != nil {
			return chat.BeginTurnResult{}, err
		}
		if linkedCount > 0 {
			return chat.BeginTurnResult{}, corruptRun("legacy pair belongs to another run key")
		}
		history, err := listCompletedHistoryBefore(ctx, tx, input.ConversationID, user.Sequence)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		return chat.BeginTurnResult{Conversation: currentConversation, History: history,
			UserMessage: user, AssistantMessage: assistant, Reused: true}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return chat.BeginTurnResult{}, err
	}

	var activeRunCount, streamingCount int64
	if err := tx.WithContext(ctx).Model(&runRow{}).
		Where("conversation_id = ? AND status IN ?", input.ConversationID,
			[]agentrun.Status{agentrun.StatusPending, agentrun.StatusRunning, agentrun.StatusWaitingTool}).
		Count(&activeRunCount).Error; err != nil {
		return chat.BeginTurnResult{}, err
	}
	if err := tx.WithContext(ctx).Model(&messageRow{}).
		Where("conversation_id = ? AND role = ? AND status = ?", input.ConversationID,
			conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).
		Count(&streamingCount).Error; err != nil {
		return chat.BeginTurnResult{}, err
	}
	if activeRunCount > 0 || streamingCount > 0 {
		return chat.BeginTurnResult{}, domainerr.ErrConflict
	}

	head, err := lazyInitHeadTx(ctx, tx, input.ConversationID, r.tree.now().UTC())
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	if head.Version < 0 || (head.ActiveLeafID != nil && head.Version == 0) {
		return chat.BeginTurnResult{}, corruptRun("invalid base head version")
	}
	var parentPath []conversation.ConversationEntry
	if head.ActiveLeafID != nil {
		parentPath, err = readPathTx(ctx, tx, input.ConversationID, *head.ActiveLeafID, r.tree.maxPathEntries)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		if !conversation.IsClosedBranchPoint(parentPath) {
			return chat.BeginTurnResult{}, domainerr.ErrConflict
		}
	}
	text := input.Text
	appended, err := r.tree.appendTxWithSnapshot(ctx, tx, conversation.AppendEntryInput{
		ConversationID: input.ConversationID,
		ExpectedHead:   conversation.HeadToken{ActiveLeafID: head.ActiveLeafID, Version: head.Version},
		Message: conversation.EntryMessage{Role: conversation.EntryRoleUser,
			Content: []conversation.EntryPart{{Type: conversation.EntryPartText, Text: &text}}},
	}, &appendSnapshot{
		head:       head,
		parentPath: parentPath,
	})
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	result, err := r.turns.beginTurnTxWithPreflight(ctx, tx, input, &beginTurnPreflight{
		conversation:                  currentConversation,
		userMessageKnownAbsent:        true,
		streamingAssistantKnownAbsent: true,
	})
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	if result.Reused {
		return chat.BeginTurnResult{}, corruptRun("unexpected reused pair after new entry append")
	}
	id, err := agentrun.NewRunID()
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	run = agentrun.Run{ID: id, ConversationID: input.ConversationID, ClientMessageID: input.ClientMessageID,
		UserMessageID: result.UserMessage.ID, AssistantMessageID: result.AssistantMessage.ID,
		BaseEntryID: head.ActiveLeafID, BaseHeadVersion: head.Version, UserEntryID: appended.Entry.ID,
		LastEntryID: appended.Entry.ID, LastClosedEntryID: appended.Entry.ID, HeadVersion: appended.Head.Version,
		Status: agentrun.StatusPending, CreatedAt: result.UserMessage.CreatedAt}
	if err := insertRunTx(ctx, tx, run); err != nil {
		return chat.BeginTurnResult{}, err
	}
	copy := run.Clone()
	result.Run = &copy
	return result, nil
}

func findRunByClientIDTx(ctx context.Context, tx *gorm.DB, conversationID, clientMessageID string) (agentrun.Run, error) {
	var row runRow
	if err := tx.WithContext(ctx).Where("conversation_id = ? AND client_message_id = ?",
		conversationID, clientMessageID).Take(&row).Error; err != nil {
		return agentrun.Run{}, err
	}
	return row.toRun()
}

func insertRunTx(ctx context.Context, tx *gorm.DB, run agentrun.Run) error {
	if err := agentrun.ValidateRun(run); err != nil {
		return err
	}
	row := runToRow(run)
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert agent run: %w", err)
	}
	return nil
}

func validAssistantTurnStatus(status conversation.MessageStatus) bool {
	return status == conversation.MessageStatusStreaming || isChatTerminalStatus(status)
}

func corruptRun(format string, args ...any) error {
	return fmt.Errorf("%w: %s", conversation.ErrEntryCorrupt, fmt.Sprintf(format, args...))
}

// Retry validation checks persisted associations, not the live head. A later
// Run or head move must not turn an otherwise valid idempotent retry into busy.
func (r *AgentRunRepository) validateRunReferencesTx(ctx context.Context, tx *gorm.DB, run agentrun.Run) (conversation.Message, conversation.Message, error) {
	load := func(id string) (conversation.Message, error) {
		return takeChatMessageTx(ctx, tx.Where("conversation_id = ? AND id = ?", run.ConversationID, id))
	}
	user, err := load(run.UserMessageID)
	if err != nil {
		return conversation.Message{}, conversation.Message{}, corruptRun("missing or invalid user message: %v", err)
	}
	assistant, err := load(run.AssistantMessageID)
	if err != nil {
		return conversation.Message{}, conversation.Message{}, corruptRun("missing or invalid assistant message: %v", err)
	}
	if user.Role != conversation.MessageRoleUser || user.Status != conversation.MessageStatusCompleted || user.ClientMessageID != run.ClientMessageID ||
		assistant.Role != conversation.MessageRoleAssistant || assistant.ClientMessageID != "" || assistant.Sequence != user.Sequence+1 || !validAssistantTurnStatus(assistant.Status) {
		return conversation.Message{}, conversation.Message{}, corruptRun("invalid run message pair")
	}
	if assistant.ErrorCode != run.ErrorCode ||
		(agentrun.IsActive(run.Status) && assistant.Status != conversation.MessageStatusStreaming) ||
		(agentrun.IsTerminal(run.Status) && string(assistant.Status) != string(run.Status)) {
		return conversation.Message{}, conversation.Message{}, corruptRun("run and assistant status disagree")
	}
	path, err := readPathTx(ctx, tx, run.ConversationID, run.LastEntryID, r.tree.maxPathEntries)
	if err != nil {
		return conversation.Message{}, conversation.Message{}, err
	}
	userIndex, closedIndex := -1, -1
	for i, entry := range path {
		if entry.ID == run.UserEntryID {
			userIndex = i
		}
		if entry.ID == run.LastClosedEntryID {
			closedIndex = i
		}
	}
	if userIndex < 0 || closedIndex < userIndex {
		return conversation.Message{}, conversation.Message{}, corruptRun("run references are outside the user path")
	}
	for _, entry := range path[userIndex+1:] {
		if entry.Message.Role == conversation.EntryRoleUser {
			return conversation.Message{}, conversation.Message{}, corruptRun("run path contains another turn's user entry")
		}
	}
	entry := path[userIndex]
	if !sameEntryID(entry.ParentID, run.BaseEntryID) || entry.Message.Role != conversation.EntryRoleUser ||
		len(entry.Message.Content) != 1 || entry.Message.Content[0].Type != conversation.EntryPartText || entry.Message.Content[0].Text == nil || *entry.Message.Content[0].Text != user.Content {
		return conversation.Message{}, conversation.Message{}, corruptRun("run user entry and message disagree")
	}
	if !conversation.IsClosedBranchPoint(path[:closedIndex+1]) {
		return conversation.Message{}, conversation.Message{}, corruptRun("run closed reference is not closed")
	}
	// A valid open path ends in one pending tool group. Its latest closed
	// prefix is immediately before that group's assistant call.
	latestClosed := len(path) - 1
	if !conversation.IsClosedBranchPoint(path) {
		for i := len(path) - 1; i >= 0; i-- {
			if path[i].Message.Role == conversation.EntryRoleAssistant && path[i].Message.StopReason == conversation.EntryStopTools {
				latestClosed = i - 1
				break
			}
		}
	}
	if closedIndex != latestClosed {
		return conversation.Message{}, conversation.Message{}, corruptRun("run closed reference is stale")
	}
	advances := int64(len(path) - userIndex)
	if agentrun.IsTerminal(run.Status) && closedIndex != len(path)-1 {
		advances++ // A terminal open branch requires one closed-head rewind.
	}
	if run.HeadVersion-run.BaseHeadVersion != advances {
		return conversation.Message{}, conversation.Message{}, corruptRun("run head version disagrees with its facts")
	}
	// Tool results are committed as a complete group. An active waiting Run
	// can only point at its assistant call, never at a partial result group.
	if run.Status == agentrun.StatusWaitingTool {
		last := path[len(path)-1].Message
		if last.Role != conversation.EntryRoleAssistant || last.StopReason != conversation.EntryStopTools {
			return conversation.Message{}, conversation.Message{}, corruptRun("waiting run does not end at an assistant call")
		}
	}
	if run.Status == agentrun.StatusCompleted {
		last := path[len(path)-1].Message
		if last.Role != conversation.EntryRoleAssistant || last.StopReason != conversation.EntryStopNormal {
			return conversation.Message{}, conversation.Message{}, corruptRun("run final entry is not a final assistant response")
		}
	}
	return user, assistant, nil
}

func sameEntryID(a, b *conversation.EntryID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// RecoverInterrupted finds all active Runs and transitions them to failed state,
// then finds all streaming assistant messages not covered by an active Run and
// transitions them to failed state. Returns counts of recovered items.
func (r *AgentRunRepository) RecoverInterrupted(ctx context.Context) (chat.RecoveryResult, error) {
	var result chat.RecoveryResult
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		// Find all active runs (pending, running, waiting_tool)
		var activeRuns []runRow
		if err := tx.WithContext(ctx).Where("status IN ?",
			[]agentrun.Status{agentrun.StatusPending, agentrun.StatusRunning, agentrun.StatusWaitingTool}).
			Find(&activeRuns).Error; err != nil {
			return fmt.Errorf("query active runs: %w", err)
		}

		// Transition each active run to failed
		for _, row := range activeRuns {
			run, err := row.toRun()
			if err != nil {
				return fmt.Errorf("decode active run %s: %w", row.ID, err)
			}
			
			// Mark the run as failed with interrupted error
			run.Status = agentrun.StatusFailed
			run.ErrorCode = "interrupted"
			now := r.tree.now().UTC()
			run.FinishedAt = &now
			
			if err := agentrun.ValidateRun(run); err != nil {
				return fmt.Errorf("validate recovered run %s: %w", run.ID, err)
			}
			
			updatedRow := runToRow(run)
			if err := tx.WithContext(ctx).Model(&runRow{}).Where("id = ?", run.ID).
				Updates(map[string]interface{}{
					"status":      updatedRow.Status,
					"error_code":  updatedRow.ErrorCode,
					"finished_at": updatedRow.FinishedAt,
				}).Error; err != nil {
				return fmt.Errorf("update run %s to failed: %w", run.ID, err)
			}
			
			// Also mark the associated assistant message as failed
			if err := tx.WithContext(ctx).Model(&messageRow{}).
				Where("id = ? AND conversation_id = ?", run.AssistantMessageID, run.ConversationID).
				Updates(map[string]interface{}{
					"status":        conversation.MessageStatusFailed,
					"error_code":    "interrupted",
					"error_message": "Message was interrupted and recovered at startup",
				}).Error; err != nil {
				return fmt.Errorf("update assistant message %s to failed: %w", run.AssistantMessageID, err)
			}
			
			result.InterruptedRuns++
		}

		// Find streaming assistant messages not covered by any run (legacy-only)
		var streamingMessages []messageRow
		if err := tx.WithContext(ctx).
			Where("role = ? AND status = ?", conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).
			Find(&streamingMessages).Error; err != nil {
			return fmt.Errorf("query streaming messages: %w", err)
		}

		for _, msg := range streamingMessages {
			// Check if this message is covered by a run
			var runCount int64
			if err := tx.WithContext(ctx).Model(&runRow{}).
				Where("assistant_message_id = ?", msg.ID).
				Count(&runCount).Error; err != nil {
				return fmt.Errorf("check run coverage for message %s: %w", msg.ID, err)
			}
			
			// If no run covers this message, it's a legacy-only streaming message
			if runCount == 0 {
				if err := tx.WithContext(ctx).Model(&messageRow{}).
					Where("id = ?", msg.ID).
					Updates(map[string]interface{}{
						"status":        conversation.MessageStatusFailed,
						"error_code":    "interrupted",
						"error_message": "Legacy message was interrupted and recovered at startup",
					}).Error; err != nil {
					return fmt.Errorf("update legacy message %s to failed: %w", msg.ID, err)
				}
				result.LegacyOnlyMessages++
			}
		}

		return nil
	})
	
	if err != nil {
		return chat.RecoveryResult{}, err
	}
	return result, nil
}
