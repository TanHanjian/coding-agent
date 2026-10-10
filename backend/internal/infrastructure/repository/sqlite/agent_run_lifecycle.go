package sqlite

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/agentrun"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

var _ chat.GenerationStore = (*AgentRunRepository)(nil)

func findRunTx(ctx context.Context, tx *gorm.DB, query *gorm.DB) (agentrun.Run, error) {
	var row runRow
	if err := query.WithContext(ctx).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return agentrun.Run{}, domainerr.ErrNotFound
		}
		return agentrun.Run{}, err
	}
	return row.toRun()
}

// saveRunTx conditions the mutation on the snapshot that authorized it. Its
// caller owns both the transaction and the provisional return value.
func saveRunTx(ctx context.Context, tx *gorm.DB, before, after agentrun.Run) error {
	if err := agentrun.ValidateRun(after); err != nil {
		return err
	}
	row := runToRow(after)
	result := tx.WithContext(ctx).Model(&runRow{}).
		Where("id = ? AND conversation_id = ? AND assistant_message_id = ? AND status = ? AND last_entry_id = ? AND head_version = ?",
			before.ID, before.ConversationID, before.AssistantMessageID, before.Status, before.LastEntryID, before.HeadVersion).
		Updates(map[string]any{
			"status": row.Status, "last_entry_id": row.LastEntryID,
			"last_closed_entry_id": row.LastClosedEntryID, "final_entry_id": row.FinalEntryID,
			"head_version": row.HeadVersion, "error_code": row.ErrorCode,
			"started_at": row.StartedAt, "finished_at": row.FinishedAt,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return domainerr.ErrConflict
	}
	return nil
}

func (r *AgentRunRepository) MarkRunning(ctx context.Context, id agentrun.RunID) (agentrun.Run, error) {
	if strings.TrimSpace(string(id)) == "" {
		return agentrun.Run{}, domainerr.ErrInvalidInput
	}
	var result agentrun.Run
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		run, err := findRunTx(ctx, tx, tx.Where("id = ?", id))
		if err != nil {
			return err
		}
		if run.Status != agentrun.StatusPending {
			return domainerr.ErrConflict
		}
		if _, _, err := r.validateRunReferencesTx(ctx, tx, run); err != nil {
			return err
		}
		result = run.Clone()
		now := r.tree.now().UTC()
		result.Status, result.StartedAt = agentrun.StatusRunning, &now
		return saveRunTx(ctx, tx, run, result)
	})
	if err != nil {
		return agentrun.Run{}, err
	}
	return result, nil
}

func (r *AgentRunRepository) GetMessage(ctx context.Context, id string) (conversation.Message, error) {
	return r.turns.GetMessage(ctx, id)
}

func (r *AgentRunRepository) AppendAssistantText(ctx context.Context, id, text string) error {
	if err := validateAssistantTextInput(id, text); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		run, err := findRunTx(ctx, tx, tx.Where("assistant_message_id = ?", id))
		if err != nil {
			return err
		}
		if !agentrun.IsActive(run.Status) {
			return domainerr.ErrConflict
		}
		// Checkpoints do not traverse the tree or demand a head token. They
		// still prove that the streaming message belongs to this active Run.
		message, err := takeChatMessageTx(ctx, tx.Where("id = ?", id))
		if err != nil {
			return err
		}
		if message.ConversationID != run.ConversationID || message.Role != conversation.MessageRoleAssistant ||
			message.Status != conversation.MessageStatusStreaming || message.ClientMessageID != "" || message.ErrorCode != run.ErrorCode {
			return corruptRun("invalid checkpoint message ownership or state")
		}
		return r.turns.appendAssistantTextTx(ctx, tx, id, text)
	})
}

// activeHeadTx compares the caller's token, the Run snapshot and the actual
// database head. It never substitutes a newly loaded token for stale intent.
func (r *AgentRunRepository) activeHeadTx(ctx context.Context, tx *gorm.DB, run agentrun.Run, expected conversation.HeadToken) (conversation.EntryHead, error) {
	if !agentrun.IsActive(run.Status) {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}
	if expected.Version != run.HeadVersion || !sameEntryID(expected.ActiveLeafID, &run.LastEntryID) {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}
	head, exists, err := loadHeadTx(ctx, tx, run.ConversationID)
	if err != nil {
		return conversation.EntryHead{}, err
	}
	if !exists {
		return conversation.EntryHead{}, corruptRun("active run has no head")
	}
	if head.Version != run.HeadVersion || !sameEntryID(head.ActiveLeafID, &run.LastEntryID) {
		return conversation.EntryHead{}, domainerr.ErrConflict
	}
	if _, _, err := r.validateRunReferencesTx(ctx, tx, run); err != nil {
		return conversation.EntryHead{}, err
	}
	return head, nil
}

func (r *AgentRunRepository) CommitRunMessages(ctx context.Context, input chat.CommitRunMessagesInput) (chat.CommitRunMessagesResult, error) {
	input = input.Clone()
	if strings.TrimSpace(string(input.RunID)) == "" || len(input.Messages) == 0 {
		return chat.CommitRunMessagesResult{}, domainerr.ErrInvalidInput
	}
	var result chat.CommitRunMessagesResult
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.commitRunMessagesTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return chat.CommitRunMessagesResult{}, err
	}
	return result, nil
}

func (r *AgentRunRepository) commitRunMessagesTx(ctx context.Context, tx *gorm.DB, input chat.CommitRunMessagesInput) (chat.CommitRunMessagesResult, error) {
	run, err := findRunTx(ctx, tx, tx.Where("id = ?", input.RunID))
	if err != nil {
		return chat.CommitRunMessagesResult{}, err
	}
	if run.Status != agentrun.StatusRunning && run.Status != agentrun.StatusWaitingTool {
		return chat.CommitRunMessagesResult{}, domainerr.ErrConflict
	}
	head, err := r.activeHeadTx(ctx, tx, run, input.ExpectedHead)
	if err != nil {
		return chat.CommitRunMessagesResult{}, err
	}
	messages := input.Messages
	if run.Status == agentrun.StatusRunning {
		if len(messages) != 1 || messages[0].Role != conversation.EntryRoleAssistant || messages[0].StopReason != conversation.EntryStopTools {
			return chat.CommitRunMessagesResult{}, domainerr.ErrInvalidInput
		}
	} else {
		var row entryNodeRow
		if err := tx.WithContext(ctx).Where("conversation_id = ? AND id = ?", run.ConversationID, run.LastEntryID).Take(&row).Error; err != nil {
			return chat.CommitRunMessagesResult{}, err
		}
		callEntry, err := row.toEntry()
		if err != nil {
			return chat.CommitRunMessagesResult{}, err
		}
		messages, err = orderRunResults(callEntry, messages)
		if err != nil {
			return chat.CommitRunMessagesResult{}, err
		}
	}
	// Validate the whole group's policy and capacity before its first write;
	// appendTx repeats these checks and validates each path continuation.
	for _, message := range messages {
		if _, _, err := r.tree.prepareAppendMessage(message); err != nil {
			return chat.CommitRunMessagesResult{}, err
		}
	}
	next := run.Clone()
	entries := make([]conversation.ConversationEntry, 0, len(messages))
	for _, message := range messages {
		appended, err := r.tree.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: run.ConversationID, ExpectedHead: conversation.HeadToken{ActiveLeafID: head.ActiveLeafID, Version: head.Version}, Message: message,
		})
		if err != nil {
			return chat.CommitRunMessagesResult{}, err
		}
		entries, head = append(entries, appended.Entry), appended.Head
	}
	next.LastEntryID, next.HeadVersion = *head.ActiveLeafID, head.Version
	if run.Status == agentrun.StatusRunning {
		next.Status = agentrun.StatusWaitingTool
	} else {
		next.Status, next.LastClosedEntryID = agentrun.StatusRunning, next.LastEntryID
	}
	if err := saveRunTx(ctx, tx, run, next); err != nil {
		return chat.CommitRunMessagesResult{}, err
	}
	return chat.CommitRunMessagesResult{Run: next, Entries: entries, Head: head}, nil
}

func orderRunResults(callEntry conversation.ConversationEntry, messages []conversation.EntryMessage) ([]conversation.EntryMessage, error) {
	if callEntry.Message.Role != conversation.EntryRoleAssistant || callEntry.Message.StopReason != conversation.EntryStopTools {
		return nil, corruptRun("waiting run does not end at an assistant call")
	}
	var calls []conversation.DeclaredToolCall
	for _, part := range callEntry.Message.Content {
		if part.Type == conversation.EntryPartToolCall && part.ToolCall != nil {
			calls = append(calls, conversation.DeclaredToolCall{ID: part.ToolCall.ID(), Name: part.ToolCall.Name()})
		}
	}
	if len(calls) == 0 || len(calls) != len(messages) {
		return nil, domainerr.ErrInvalidInput
	}
	byID := make(map[string]conversation.EntryMessage, len(messages))
	for _, message := range messages {
		if err := conversation.ValidateEntryMessage(message); err != nil {
			return nil, err
		}
		if message.Role != conversation.EntryRoleToolResult || message.ToolResult.CallEntryID != callEntry.ID {
			return nil, domainerr.ErrConflict
		}
		id := message.ToolResult.CallID
		if _, exists := byID[id]; exists {
			return nil, domainerr.ErrInvalidInput
		}
		byID[id] = message
	}
	ordered := make([]conversation.EntryMessage, len(calls))
	for i, call := range calls {
		message, exists := byID[call.ID]
		if !exists || message.ToolResult.ToolName != call.Name {
			return nil, domainerr.ErrConflict
		}
		ordered[i] = message
	}
	return ordered, nil
}

func (r *AgentRunRepository) FinishAssistant(ctx context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
	input = input.Clone()
	if strings.TrimSpace(input.AssistantMessageID) == "" || !isChatTerminalStatus(input.Status) {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	input.AssistantMessageID = strings.TrimSpace(input.AssistantMessageID)
	var result conversation.Message
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.finishRunAssistantTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return conversation.Message{}, err
	}
	return result, nil
}

func (r *AgentRunRepository) finishRunAssistantTx(ctx context.Context, tx *gorm.DB, input chat.FinishAssistantInput) (conversation.Message, error) {
	message, err := takeChatMessageTx(ctx, tx.Where("id = ?", input.AssistantMessageID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return conversation.Message{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Message{}, err
	}
	if message.Role != conversation.MessageRoleAssistant {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	run, err := findRunTx(ctx, tx, tx.Where("assistant_message_id = ?", message.ID))
	if errors.Is(err, domainerr.ErrNotFound) {
		// Legacy records are readable/idempotent only. This adapter must not
		// manufacture a Run or finalize an unowned streaming generation.
		if isChatTerminalStatus(message.Status) && message.Status == input.Status {
			return message, nil
		}
		return conversation.Message{}, domainerr.ErrConflict
	}
	if err != nil {
		return conversation.Message{}, err
	}
	if agentrun.IsTerminal(run.Status) {
		if _, _, err := r.validateRunReferencesTx(ctx, tx, run); err != nil {
			return conversation.Message{}, err
		}
		if string(input.Status) != string(run.Status) {
			return conversation.Message{}, domainerr.ErrConflict
		}
		return message, nil
	}
	if err := validateFinishAssistantInput(input); err != nil {
		return conversation.Message{}, err
	}
	if input.ExpectedHead == nil {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	head, err := r.activeHeadTx(ctx, tx, run, *input.ExpectedHead)
	if err != nil {
		return conversation.Message{}, err
	}
	next := run.Clone()
	if input.Status == conversation.MessageStatusCompleted {
		if run.Status != agentrun.StatusRunning {
			return conversation.Message{}, domainerr.ErrConflict
		}
		if input.FinalMessage == nil || input.FinalMessage.Role != conversation.EntryRoleAssistant || input.FinalMessage.StopReason != conversation.EntryStopNormal {
			return conversation.Message{}, domainerr.ErrInvalidInput
		}
		appended, err := r.tree.appendTx(ctx, tx, conversation.AppendEntryInput{
			ConversationID: run.ConversationID, ExpectedHead: *input.ExpectedHead, Message: *input.FinalMessage,
		})
		if err != nil {
			return conversation.Message{}, err
		}
		head = appended.Head
		next.LastEntryID, next.LastClosedEntryID = appended.Entry.ID, appended.Entry.ID
		next.FinalEntryID = &appended.Entry.ID
	} else {
		if input.FinalMessage != nil {
			return conversation.Message{}, domainerr.ErrInvalidInput
		}
		if run.LastEntryID != run.LastClosedEntryID {
			head, err = r.tree.moveHeadTx(ctx, tx, conversation.MoveHeadInput{
				ConversationID: run.ConversationID, ExpectedHead: *input.ExpectedHead, TargetID: &run.LastClosedEntryID,
			})
			if err != nil {
				return conversation.Message{}, err
			}
		}
	}
	now := r.tree.now().UTC()
	next.Status, next.ErrorCode, next.FinishedAt, next.HeadVersion = agentrun.Status(input.Status), input.ErrorCode, &now, head.Version
	if err := saveRunTx(ctx, tx, run, next); err != nil {
		return conversation.Message{}, err
	}
	// ErrorMessage is the application's controlled code-to-text mapping per
	// GenerationStore's contract; raw provider errors are never an input here.
	return r.turns.finishAssistantTx(ctx, tx, input)
}
