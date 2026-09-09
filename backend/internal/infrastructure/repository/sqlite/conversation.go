package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/infrastructure/repository"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// ConversationRepository stores user-visible conversation containers.
type ConversationRepository struct{ db *storage.DB }

func NewConversationRepository(db *storage.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

func (r *ConversationRepository) Create(ctx context.Context, record conversation.Conversation) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO conversations (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		record.ID, record.Title, formatTime(record.CreatedAt), formatTime(record.UpdatedAt))
	if err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

func (r *ConversationRepository) GetByID(ctx context.Context, id string) (conversation.Conversation, error) {
	record, err := scanConversation(r.db.QueryRowContext(ctx, `SELECT id, title, created_at, updated_at FROM conversations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return conversation.Conversation{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return record, nil
}

func (r *ConversationRepository) List(ctx context.Context) ([]conversation.Conversation, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, title, created_at, updated_at FROM conversations ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	records := make([]conversation.Conversation, 0)
	for rows.Next() {
		record, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversations: %w", err)
	}
	return records, nil
}

func (r *ConversationRepository) UpdateTitle(ctx context.Context, id, title string, updatedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE conversations SET title = ?, updated_at = ? WHERE id = ?`, title, formatTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update conversation title: %w", err)
	}
	return requireAffected(result, "update conversation title")
}

func (r *ConversationRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return requireAffected(result, "delete conversation")
}

// MessageRepository stores user-visible messages and allocates their order.
type MessageRepository struct{ db *storage.DB }

func NewMessageRepository(db *storage.DB) *MessageRepository { return &MessageRepository{db: db} }

func (r *MessageRepository) Create(ctx context.Context, record conversation.Message) (conversation.Message, error) {
	if !isInitialMessage(record) {
		return conversation.Message{}, domainerr.ErrInvalidInput
	}
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM conversations WHERE id = ?)`, record.ConversationID).Scan(&exists); err != nil {
			return fmt.Errorf("check conversation: %w", err)
		}
		if !exists {
			return domainerr.ErrNotFound
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM messages WHERE conversation_id = ?`, record.ConversationID).Scan(&record.Sequence); err != nil {
			return fmt.Errorf("allocate message sequence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages (
			id, conversation_id, sequence, role, content, status, error_code, error_message, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.ConversationID, record.Sequence, record.Role, record.Content, record.Status,
			record.ErrorCode, record.ErrorMessage, formatTime(record.CreatedAt), formatTime(record.UpdatedAt)); err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
		_, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(record.UpdatedAt), record.ConversationID)
		return err
	})
	if err != nil {
		return conversation.Message{}, err
	}
	return record, nil
}

func (r *MessageRepository) GetByID(ctx context.Context, id string) (conversation.Message, error) {
	record, err := scanMessage(r.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return conversation.Message{}, domainerr.ErrNotFound
	}
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get message: %w", err)
	}
	return record, nil
}

func (r *MessageRepository) ListByConversation(ctx context.Context, conversationID string) ([]conversation.Message, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE conversation_id = ? ORDER BY sequence ASC`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	records := make([]conversation.Message, 0)
	for rows.Next() {
		record, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return records, nil
}

func (r *MessageRepository) UpdateAssistant(ctx context.Context, record conversation.Message) error {
	if record.Role != conversation.MessageRoleAssistant || !isTerminalStatus(record.Status) {
		return domainerr.ErrInvalidInput
	}
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var role conversation.MessageRole
		var status conversation.MessageStatus
		err := tx.QueryRowContext(ctx, `SELECT role, status FROM messages WHERE id = ? AND conversation_id = ?`, record.ID, record.ConversationID).Scan(&role, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return domainerr.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get assistant message state: %w", err)
		}
		if role != conversation.MessageRoleAssistant || status != conversation.MessageStatusStreaming {
			return domainerr.ErrInvalidInput
		}
		result, err := tx.ExecContext(ctx, `UPDATE messages
			SET content = ?, status = ?, error_code = ?, error_message = ?, updated_at = ?
			WHERE id = ? AND conversation_id = ?`,
			record.Content, record.Status, record.ErrorCode, record.ErrorMessage, formatTime(record.UpdatedAt), record.ID, record.ConversationID)
		if err != nil {
			return fmt.Errorf("update assistant message: %w", err)
		}
		if err := requireAffected(result, "update assistant message"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(record.UpdatedAt), record.ConversationID)
		return err
	})
}

func (r *MessageRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var conversationID string
		if err := tx.QueryRowContext(ctx, `SELECT conversation_id FROM messages WHERE id = ?`, id).Scan(&conversationID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domainerr.ErrNotFound
			}
			return fmt.Errorf("get message conversation: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete message: %w", err)
		}
		if err := requireAffected(result, "delete message"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at = ? WHERE id = ?`, formatTime(time.Now()), conversationID)
		return err
	})
}

const messageColumns = `id, conversation_id, client_message_id, sequence, role, content, status, error_code, error_message, created_at, updated_at`

func scanConversation(row interface{ Scan(...any) error }) (conversation.Conversation, error) {
	var record conversation.Conversation
	var createdAt, updatedAt string
	if err := row.Scan(&record.ID, &record.Title, &createdAt, &updatedAt); err != nil {
		return conversation.Conversation{}, err
	}
	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("parse conversation created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("parse conversation updated_at: %w", err)
	}
	return record, nil
}

func scanMessage(row interface{ Scan(...any) error }) (conversation.Message, error) {
	var record conversation.Message
	var createdAt, updatedAt string
	if err := row.Scan(&record.ID, &record.ConversationID, &record.ClientMessageID, &record.Sequence, &record.Role, &record.Content, &record.Status, &record.ErrorCode, &record.ErrorMessage, &createdAt, &updatedAt); err != nil {
		return conversation.Message{}, err
	}
	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return conversation.Message{}, fmt.Errorf("parse message created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return conversation.Message{}, fmt.Errorf("parse message updated_at: %w", err)
	}
	return record, nil
}

func isInitialMessage(record conversation.Message) bool {
	return (record.Role == conversation.MessageRoleUser && record.Status == conversation.MessageStatusCompleted && record.ErrorCode == "" && record.ErrorMessage == "") ||
		(record.Role == conversation.MessageRoleAssistant && record.Status == conversation.MessageStatusStreaming)
}

func isTerminalStatus(status conversation.MessageStatus) bool {
	return status == conversation.MessageStatusCompleted || status == conversation.MessageStatusCancelled || status == conversation.MessageStatusFailed
}

func requireAffected(result sql.Result, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check %s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return domainerr.ErrNotFound
	}
	return nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

var _ repository.ConversationRepository = (*ConversationRepository)(nil)
var _ repository.MessageRepository = (*MessageRepository)(nil)
