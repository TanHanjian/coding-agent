package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

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
	row := conversationToRow(record)
	if err := r.db.ORM(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert conversation: %w", err)
	}
	return nil
}

func (r *ConversationRepository) GetByID(ctx context.Context, id string) (conversation.Conversation, error) {
	var row conversationRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return conversation.Conversation{}, domainerr.ErrNotFound
		}
		return conversation.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	record, err := row.toConversation()
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return record, nil
}

func (r *ConversationRepository) List(ctx context.Context) ([]conversation.Conversation, error) {
	var rows []conversationRow
	if err := r.db.ORM(ctx).Order("updated_at DESC").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	records := make([]conversation.Conversation, 0, len(rows))
	for _, row := range rows {
		record, err := row.toConversation()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (r *ConversationRepository) UpdateTitle(ctx context.Context, id, title string, updatedAt time.Time) error {
	result := r.db.ORM(ctx).Model(&conversationRow{}).Where("id = ?", id).Updates(map[string]any{
		"title": title, "updated_at": formatTime(updatedAt),
	})
	return requireORMAffected(result, "update conversation title")
}

func (r *ConversationRepository) Delete(ctx context.Context, id string) error {
	result := r.db.ORM(ctx).Where("id = ?", id).Delete(&conversationRow{})
	return requireORMAffected(result, "delete conversation")
}

// MessageRepository stores user-visible messages and allocates their order.
type MessageRepository struct{ db *storage.DB }

func NewMessageRepository(db *storage.DB) *MessageRepository { return &MessageRepository{db: db} }

func (r *MessageRepository) GetByID(ctx context.Context, id string) (conversation.Message, error) {
	var row messageRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return conversation.Message{}, domainerr.ErrNotFound
		}
		return conversation.Message{}, fmt.Errorf("get message: %w", err)
	}
	record, err := row.toMessage()
	if err != nil {
		return conversation.Message{}, fmt.Errorf("get message: %w", err)
	}
	return record, nil
}

func (r *MessageRepository) ListByConversation(ctx context.Context, conversationID string) ([]conversation.Message, error) {
	var rows []messageRow
	if err := r.db.ORM(ctx).Where("conversation_id = ?", conversationID).Order("sequence ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	records := make([]conversation.Message, 0, len(rows))
	for _, row := range rows {
		record, err := row.toMessage()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (r *MessageRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		var row messageRow
		if err := tx.Select("conversation_id").Where("id = ?", id).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return domainerr.ErrNotFound
			}
			return fmt.Errorf("get message conversation: %w", err)
		}
		if err := requireORMAffected(tx.Where("id = ?", id).Delete(&messageRow{}), "delete message"); err != nil {
			return err
		}
		return tx.Model(&conversationRow{}).Where("id = ?", row.ConversationID).
			Update("updated_at", formatTime(time.Now())).Error
	})
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

var _ repository.ConversationRepository = (*ConversationRepository)(nil)
var _ repository.MessageRepository = (*MessageRepository)(nil)
