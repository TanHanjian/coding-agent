package sqlite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// ChatTurnRepository 是聊天写模型的独立实现位置。普通 MessageRepository 不应
// 承担 BeginTurn 的幂等和并发语义。
type ChatTurnRepository struct {
	db *storage.DB
}

func NewChatTurnRepository(db *storage.DB) *ChatTurnRepository {
	return &ChatTurnRepository{db: db}
}

// BeginTurn 在同一个 SQLite 事务中完成幂等检查、消息对创建和历史读取。
// 若相同 client_message_id 已存在，则只返回既有消息对，不创建任何新 Message。
func (r *ChatTurnRepository) BeginTurn(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	if err := validateBeginTurnInput(input); err != nil {
		return chat.BeginTurnResult{}, err
	}

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

func validateBeginTurnInput(input chat.BeginTurnInput) error {
	if strings.TrimSpace(input.ConversationID) == "" || strings.TrimSpace(input.ClientMessageID) == "" || strings.TrimSpace(input.Text) == "" {
		return domainerr.ErrInvalidInput
	}
	return nil
}

// beginTurnPreflight contains facts established by a caller that is already
// operating in the same transaction. It is only for private composition.
type beginTurnPreflight struct {
	conversation                  conversation.Conversation
	userMessageKnownAbsent        bool
	streamingAssistantKnownAbsent bool
}

// beginTurnTx only uses the caller's transaction. Its result is provisional
// until commit succeeds; any error must abort the entire transaction.
func (r *ChatTurnRepository) beginTurnTx(ctx context.Context, tx *gorm.DB, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	return r.beginTurnTxWithPreflight(ctx, tx, input, nil)
}

func (r *ChatTurnRepository) beginTurnTxWithPreflight(ctx context.Context, tx *gorm.DB, input chat.BeginTurnInput, preflight *beginTurnPreflight) (chat.BeginTurnResult, error) {
	if err := validateBeginTurnInput(input); err != nil {
		return chat.BeginTurnResult{}, err
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	clientMessageID := strings.TrimSpace(input.ClientMessageID)
	var currentConversation conversation.Conversation
	var err error
	if preflight == nil {
		currentConversation, err = loadConversationForTurn(ctx, tx, conversationID)
	} else {
		currentConversation = preflight.conversation
	}
	if err != nil {
		return chat.BeginTurnResult{}, err
	}

	var userMessage conversation.Message
	if preflight == nil || !preflight.userMessageKnownAbsent {
		userMessage, err = findUserMessageByClientID(ctx, tx, conversationID, clientMessageID)
	} else {
		err = gorm.ErrRecordNotFound
	}
	if err == nil {
		assistantMessage, err := findPairedAssistantMessage(ctx, tx, userMessage)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		history, err := listCompletedHistoryBefore(ctx, tx, conversationID, userMessage.Sequence)
		if err != nil {
			return chat.BeginTurnResult{}, err
		}
		return chat.BeginTurnResult{
			Conversation:     currentConversation,
			History:          history,
			UserMessage:      userMessage,
			AssistantMessage: assistantMessage,
			Reused:           true,
		}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return chat.BeginTurnResult{}, err
	}

	var activeCount int64
	if preflight == nil || !preflight.streamingAssistantKnownAbsent {
		if err := tx.WithContext(ctx).Model(&messageRow{}).
			Where("conversation_id = ? AND role = ? AND status = ?", conversationID, conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).
			Count(&activeCount).Error; err != nil {
			return chat.BeginTurnResult{}, fmt.Errorf("check active chat generation: %w", err)
		}
	}
	if activeCount > 0 {
		return chat.BeginTurnResult{}, domainerr.ErrConflict
	}

	var nextSequence int
	if err := tx.WithContext(ctx).Model(&messageRow{}).Select("COALESCE(MAX(sequence), 0) + 1").
		Where("conversation_id = ?", conversationID).Scan(&nextSequence).Error; err != nil {
		return chat.BeginTurnResult{}, fmt.Errorf("allocate chat message sequence: %w", err)
	}
	history, err := listCompletedHistoryBefore(ctx, tx, conversationID, nextSequence)
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	now := time.Now().UTC()
	userID, err := newChatMessageID()
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	assistantID, err := newChatMessageID()
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	userMessage = conversation.Message{
		ID: userID, ConversationID: conversationID, ClientMessageID: clientMessageID,
		Sequence: nextSequence, Role: conversation.MessageRoleUser, Content: input.Text,
		Status: conversation.MessageStatusCompleted, CreatedAt: now, UpdatedAt: now,
	}
	assistantMessage := conversation.Message{
		ID: assistantID, ConversationID: conversationID, Sequence: nextSequence + 1,
		Role: conversation.MessageRoleAssistant, Status: conversation.MessageStatusStreaming,
		CreatedAt: now, UpdatedAt: now,
	}
	pair := []messageRow{messageToRow(userMessage), messageToRow(assistantMessage)}
	if err := tx.WithContext(ctx).Create(&pair).Error; err != nil {
		return chat.BeginTurnResult{}, fmt.Errorf("insert chat messages: %w", err)
	}
	if err := tx.WithContext(ctx).Model(&conversationRow{}).Where("id = ?", conversationID).
		Update("updated_at", formatTime(now)).Error; err != nil {
		return chat.BeginTurnResult{}, fmt.Errorf("touch conversation after beginning chat: %w", err)
	}
	currentConversation.UpdatedAt = now
	return chat.BeginTurnResult{
		Conversation:     currentConversation,
		History:          history,
		UserMessage:      userMessage,
		AssistantMessage: assistantMessage,
	}, nil
}

func loadConversationForTurn(ctx context.Context, tx *gorm.DB, conversationID string) (conversation.Conversation, error) {
	var row conversationRow
	if err := tx.WithContext(ctx).Where("id = ?", conversationID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return conversation.Conversation{}, domainerr.ErrNotFound
		}
		return conversation.Conversation{}, fmt.Errorf("get chat conversation: %w", err)
	}
	record, err := row.toConversation()
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("get chat conversation: %w", err)
	}
	return record, nil
}

// takeChatMessageTx preserves ORM not-found internally and leaves domain error
// translation to the caller. The supplied handle already carries its filters.
func takeChatMessageTx(ctx context.Context, tx *gorm.DB) (conversation.Message, error) {
	var row messageRow
	if err := tx.WithContext(ctx).Take(&row).Error; err != nil {
		return conversation.Message{}, err
	}
	return row.toMessage()
}

func findUserMessageByClientID(ctx context.Context, tx *gorm.DB, conversationID, clientMessageID string) (conversation.Message, error) {
	return takeChatMessageTx(ctx, tx.Where("conversation_id = ? AND client_message_id = ? AND role = ?",
		conversationID, clientMessageID, conversation.MessageRoleUser))
}

func findPairedAssistantMessage(ctx context.Context, tx *gorm.DB, userMessage conversation.Message) (conversation.Message, error) {
	record, err := takeChatMessageTx(ctx, tx.Where("conversation_id = ? AND sequence = ? AND role = ?",
		userMessage.ConversationID, userMessage.Sequence+1, conversation.MessageRoleAssistant))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return conversation.Message{}, fmt.Errorf("chat turn is incomplete for user message %q", userMessage.ID)
	}
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get paired assistant message: %w", err)
	}
	return record, nil
}

func listCompletedHistoryBefore(ctx context.Context, tx *gorm.DB, conversationID string, beforeSequence int) ([]conversation.Message, error) {
	var rows []messageRow
	if err := tx.WithContext(ctx).Where("conversation_id = ? AND sequence < ? AND status = ?",
		conversationID, beforeSequence, conversation.MessageStatusCompleted).Order("sequence ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list completed chat history: %w", err)
	}
	history := make([]conversation.Message, 0, len(rows))
	for _, row := range rows {
		record, err := row.toMessage()
		if err != nil {
			return nil, err
		}
		history = append(history, record)
	}
	return history, nil
}

func newChatMessageID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate chat message id: %w", err)
	}
	return "m_" + hex.EncodeToString(bytes[:]), nil
}

// AppendAssistantText 将一批模型文本原子地追加到 SQLite 的当前消息全文。
// 生成已取消或结束后，任何迟到的 chunk 都返回冲突，不能污染最终消息。
func (r *ChatTurnRepository) AppendAssistantText(ctx context.Context, assistantMessageID string, text string) error {
	if err := validateAssistantTextInput(assistantMessageID, text); err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		return r.appendAssistantTextTx(ctx, tx, assistantMessageID, text)
	})
}

func validateAssistantTextInput(assistantMessageID, text string) error {
	if strings.TrimSpace(assistantMessageID) == "" || text == "" {
		return domainerr.ErrInvalidInput
	}
	return nil
}

// appendAssistantTextTx uses only tx; any error must abort the entire transaction.
func (r *ChatTurnRepository) appendAssistantTextTx(ctx context.Context, tx *gorm.DB, assistantMessageID string, text string) error {
	if err := validateAssistantTextInput(assistantMessageID, text); err != nil {
		return err
	}
	message, err := takeChatMessageTx(ctx, tx.Where("id = ?", assistantMessageID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domainerr.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get assistant message for text append: %w", err)
	}
	if message.Role != conversation.MessageRoleAssistant || message.Status != conversation.MessageStatusStreaming {
		return domainerr.ErrConflict
	}

	now := time.Now().UTC()
	result := tx.WithContext(ctx).Model(&messageRow{}).
		Where("id = ? AND role = ? AND status = ?", assistantMessageID, conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).
		Updates(map[string]any{"content": message.Content + text, "updated_at": formatTime(now)})
	if result.Error != nil {
		return fmt.Errorf("append assistant message text: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return domainerr.ErrConflict
	}
	return nil
}

// FinishAssistant 将 streaming assistant Message 原子切换到唯一终态，并保留
// 已累积的内容。重复取消已取消消息是幂等的，其余终态竞争返回冲突。
func (r *ChatTurnRepository) FinishAssistant(ctx context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
	if err := validateFinishAssistantInput(input); err != nil {
		return conversation.Message{}, err
	}

	var result conversation.Message
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		var err error
		result, err = r.finishAssistantTx(ctx, tx, input)
		return err
	})
	if err != nil {
		return conversation.Message{}, err
	}
	return result, nil
}

func validateFinishAssistantInput(input chat.FinishAssistantInput) error {
	if strings.TrimSpace(input.AssistantMessageID) == "" || !isChatTerminalStatus(input.Status) {
		return domainerr.ErrInvalidInput
	}
	if input.Status != conversation.MessageStatusFailed && (input.ErrorCode != "" || input.ErrorMessage != "") {
		return domainerr.ErrInvalidInput
	}
	return nil
}

// finishAssistantTx returns a provisional message, valid only after commit.
// Any error must abort the entire transaction. Only repeated cancellation is idempotent.
func (r *ChatTurnRepository) finishAssistantTx(ctx context.Context, tx *gorm.DB, input chat.FinishAssistantInput) (conversation.Message, error) {
	if err := validateFinishAssistantInput(input); err != nil {
		return conversation.Message{}, err
	}
	assistantMessageID := strings.TrimSpace(input.AssistantMessageID)
	message, err := takeChatMessageTx(ctx, tx.Where("id = ?", assistantMessageID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return conversation.Message{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get assistant message for finish: %w", err)
	}
	if message.Role != conversation.MessageRoleAssistant {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	if message.Status != conversation.MessageStatusStreaming {
		if input.Status == conversation.MessageStatusCancelled && message.Status == conversation.MessageStatusCancelled {
			return message, nil
		}
		return conversation.Message{}, domainerr.ErrConflict
	}

	now := time.Now().UTC()
	update := tx.WithContext(ctx).Model(&messageRow{}).
		Where("id = ? AND role = ? AND status = ?", assistantMessageID, conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).
		Updates(map[string]any{
			"status": input.Status, "error_code": input.ErrorCode,
			"error_message": input.ErrorMessage, "updated_at": formatTime(now),
		})
	if update.Error != nil {
		return conversation.Message{}, fmt.Errorf("finish assistant message: %w", update.Error)
	}
	if update.RowsAffected != 1 {
		return conversation.Message{}, domainerr.ErrConflict
	}
	if err := tx.WithContext(ctx).Model(&conversationRow{}).Where("id = ?", message.ConversationID).
		Update("updated_at", formatTime(now)).Error; err != nil {
		return conversation.Message{}, fmt.Errorf("touch conversation after finishing chat: %w", err)
	}
	message.Status = input.Status
	message.ErrorCode = input.ErrorCode
	message.ErrorMessage = input.ErrorMessage
	message.UpdatedAt = now
	return message, nil
}

// GetMessage 供取消和恢复流程读取当前持久化状态。
func (r *ChatTurnRepository) GetMessage(ctx context.Context, messageID string) (conversation.Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	record, err := takeChatMessageTx(ctx, r.db.ORM(ctx).Where("id = ?", messageID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return conversation.Message{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get chat message: %w", err)
	}
	return record, nil
}

func isChatTerminalStatus(status conversation.MessageStatus) bool {
	return status == conversation.MessageStatusCompleted || status == conversation.MessageStatusCancelled || status == conversation.MessageStatusFailed
}

var _ chat.TurnStore = (*ChatTurnRepository)(nil)
