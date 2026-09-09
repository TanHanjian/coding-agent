package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// ChatTurnRepository 是聊天写模型的独立实现位置。普通 MessageRepository 不应
// 承担 BeginTurn 的幂等和并发语义；后续实现仅在此文件内补全事务 SQL。
type ChatTurnRepository struct {
	db *storage.DB
}

func NewChatTurnRepository(db *storage.DB) *ChatTurnRepository {
	return &ChatTurnRepository{db: db}
}

// BeginTurn 在同一个 SQLite 事务中完成幂等检查、消息对创建和历史读取。
// 若相同 client_message_id 已存在，则只返回既有消息对，不创建任何新 Message。
func (r *ChatTurnRepository) BeginTurn(ctx context.Context, input chat.BeginTurnInput) (chat.BeginTurnResult, error) {
	conversationID := strings.TrimSpace(input.ConversationID)
	clientMessageID := strings.TrimSpace(input.ClientMessageID)
	if conversationID == "" || clientMessageID == "" || strings.TrimSpace(input.Text) == "" {
		return chat.BeginTurnResult{}, domainerr.ErrInvalidInput
	}

	var result chat.BeginTurnResult
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		currentConversation, err := loadConversationForTurn(ctx, tx, conversationID)
		if err != nil {
			return err
		}

		userMessage, err := findUserMessageByClientID(ctx, tx, conversationID, clientMessageID)
		if err == nil {
			assistantMessage, err := findPairedAssistantMessage(ctx, tx, userMessage)
			if err != nil {
				return err
			}
			history, err := listCompletedHistoryBefore(ctx, tx, conversationID, userMessage.Sequence)
			if err != nil {
				return err
			}
			result = chat.BeginTurnResult{
				Conversation:     currentConversation,
				History:          history,
				UserMessage:      userMessage,
				AssistantMessage: assistantMessage,
				Reused:           true,
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM messages WHERE conversation_id = ? AND role = ? AND status = ?
		)`, conversationID, conversation.MessageRoleAssistant, conversation.MessageStatusStreaming).Scan(&active); err != nil {
			return fmt.Errorf("check active chat generation: %w", err)
		}
		if active {
			return domainerr.ErrConflict
		}

		var nextSequence int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM messages WHERE conversation_id = ?`, conversationID).Scan(&nextSequence); err != nil {
			return fmt.Errorf("allocate chat message sequence: %w", err)
		}
		history, err := listCompletedHistoryBefore(ctx, tx, conversationID, nextSequence)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		userID, err := newChatMessageID()
		if err != nil {
			return err
		}
		assistantID, err := newChatMessageID()
		if err != nil {
			return err
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages (
			id, conversation_id, client_message_id, sequence, role, content, status, error_code, error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '', '', ?, ?)`,
			userMessage.ID, userMessage.ConversationID, userMessage.ClientMessageID, userMessage.Sequence,
			userMessage.Role, userMessage.Content, userMessage.Status, formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("insert chat user message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages (
			id, conversation_id, client_message_id, sequence, role, content, status, error_code, error_message, created_at, updated_at
		) VALUES (?, ?, '', ?, ?, '', ?, '', '', ?, ?)`,
			assistantMessage.ID, assistantMessage.ConversationID, assistantMessage.Sequence,
			assistantMessage.Role, assistantMessage.Status, formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("insert chat assistant message: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(now), conversationID); err != nil {
			return fmt.Errorf("touch conversation after beginning chat: %w", err)
		}
		currentConversation.UpdatedAt = now
		result = chat.BeginTurnResult{
			Conversation:     currentConversation,
			History:          history,
			UserMessage:      userMessage,
			AssistantMessage: assistantMessage,
		}
		return nil
	})
	if err != nil {
		return chat.BeginTurnResult{}, err
	}
	return result, nil
}

func loadConversationForTurn(ctx context.Context, tx *sql.Tx, conversationID string) (conversation.Conversation, error) {
	record, err := scanConversation(tx.QueryRowContext(ctx, `SELECT id, title, created_at, updated_at FROM conversations WHERE id = ?`, conversationID))
	if errors.Is(err, sql.ErrNoRows) {
		return conversation.Conversation{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("get chat conversation: %w", err)
	}
	return record, nil
}

func findUserMessageByClientID(ctx context.Context, tx *sql.Tx, conversationID, clientMessageID string) (conversation.Message, error) {
	record, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE conversation_id = ? AND client_message_id = ? AND role = ?`, conversationID, clientMessageID, conversation.MessageRoleUser))
	if err != nil {
		return conversation.Message{}, err
	}
	return record, nil
}

func findPairedAssistantMessage(ctx context.Context, tx *sql.Tx, userMessage conversation.Message) (conversation.Message, error) {
	record, err := scanMessage(tx.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE conversation_id = ? AND sequence = ? AND role = ?`, userMessage.ConversationID, userMessage.Sequence+1, conversation.MessageRoleAssistant))
	if errors.Is(err, sql.ErrNoRows) {
		return conversation.Message{}, fmt.Errorf("chat turn is incomplete for user message %q", userMessage.ID)
	}
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get paired assistant message: %w", err)
	}
	return record, nil
}

func listCompletedHistoryBefore(ctx context.Context, tx *sql.Tx, conversationID string, beforeSequence int) ([]conversation.Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages
		WHERE conversation_id = ? AND sequence < ? AND status = ? ORDER BY sequence ASC`, conversationID, beforeSequence, conversation.MessageStatusCompleted)
	if err != nil {
		return nil, fmt.Errorf("list completed chat history: %w", err)
	}
	defer rows.Close()
	history := make([]conversation.Message, 0)
	for rows.Next() {
		record, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		history = append(history, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate completed chat history: %w", err)
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
	if strings.TrimSpace(assistantMessageID) == "" || text == "" {
		return domainerr.ErrInvalidInput
	}

	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		message, err := scanMessage(tx.QueryRowContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE id = ?`, assistantMessageID))
		if errors.Is(err, sql.ErrNoRows) {
			return domainerr.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get assistant message for text append: %w", err)
		}
		if message.Role != conversation.MessageRoleAssistant || message.Status != conversation.MessageStatusStreaming {
			return domainerr.ErrConflict
		}

		now := time.Now().UTC()
		result, err := tx.ExecContext(ctx, `UPDATE messages
			SET content = ?, updated_at = ?
			WHERE id = ? AND role = ? AND status = ?`,
			message.Content+text, formatTime(now), assistantMessageID,
			conversation.MessageRoleAssistant, conversation.MessageStatusStreaming)
		if err != nil {
			return fmt.Errorf("append assistant message text: %w", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check assistant text append result: %w", err)
		}
		if updated != 1 {
			return domainerr.ErrConflict
		}
		return nil
	})
	return err
}

// FinishAssistant 将 streaming assistant Message 原子切换到唯一终态，并保留
// 已累积的内容。重复取消已取消消息是幂等的，其余终态竞争返回冲突。
func (r *ChatTurnRepository) FinishAssistant(ctx context.Context, input chat.FinishAssistantInput) (conversation.Message, error) {
	assistantMessageID := strings.TrimSpace(input.AssistantMessageID)
	if assistantMessageID == "" || !isChatTerminalStatus(input.Status) {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	if input.Status != conversation.MessageStatusFailed && (input.ErrorCode != "" || input.ErrorMessage != "") {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}

	var result conversation.Message
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		message, err := scanMessage(tx.QueryRowContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE id = ?`, assistantMessageID))
		if errors.Is(err, sql.ErrNoRows) {
			return domainerr.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get assistant message for finish: %w", err)
		}
		if message.Role != conversation.MessageRoleAssistant {
			return domainerr.ErrInvalidInput
		}
		if message.Status != conversation.MessageStatusStreaming {
			if input.Status == conversation.MessageStatusCancelled && message.Status == conversation.MessageStatusCancelled {
				result = message
				return nil
			}
			return domainerr.ErrConflict
		}

		now := time.Now().UTC()
		update, err := tx.ExecContext(ctx, `UPDATE messages
			SET status = ?, error_code = ?, error_message = ?, updated_at = ?
			WHERE id = ? AND role = ? AND status = ?`,
			input.Status, input.ErrorCode, input.ErrorMessage, formatTime(now), assistantMessageID,
			conversation.MessageRoleAssistant, conversation.MessageStatusStreaming)
		if err != nil {
			return fmt.Errorf("finish assistant message: %w", err)
		}
		updated, err := update.RowsAffected()
		if err != nil {
			return fmt.Errorf("check assistant finish result: %w", err)
		}
		if updated != 1 {
			return domainerr.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(now), message.ConversationID); err != nil {
			return fmt.Errorf("touch conversation after finishing chat: %w", err)
		}
		message.Status = input.Status
		message.ErrorCode = input.ErrorCode
		message.ErrorMessage = input.ErrorMessage
		message.UpdatedAt = now
		result = message
		return nil
	})
	if err != nil {
		return conversation.Message{}, err
	}
	return result, nil
}

// GetMessage 供取消和恢复流程读取当前持久化状态。
func (r *ChatTurnRepository) GetMessage(ctx context.Context, messageID string) (conversation.Message, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	record, err := scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, messageID))
	if errors.Is(err, sql.ErrNoRows) {
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
