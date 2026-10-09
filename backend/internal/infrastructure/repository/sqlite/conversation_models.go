package sqlite

import (
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// conversationRow mirrors the existing schema without ORM associations or clocks.
type conversationRow struct {
	ID        string `gorm:"column:id;primaryKey"`
	Title     string `gorm:"column:title"`
	CreatedAt string `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt string `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (conversationRow) TableName() string { return "conversations" }

func conversationToRow(record conversation.Conversation) conversationRow {
	return conversationRow{
		ID: record.ID, Title: record.Title,
		CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
	}
}

func (row conversationRow) toConversation() (conversation.Conversation, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("parse conversation created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return conversation.Conversation{}, fmt.Errorf("parse conversation updated_at: %w", err)
	}
	return conversation.Conversation{
		ID: row.ID, Title: row.Title, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

// messageRow is also shared by the ChatTurn transaction migration.
type messageRow struct {
	ID              string `gorm:"column:id;primaryKey"`
	ConversationID  string `gorm:"column:conversation_id"`
	ClientMessageID string `gorm:"column:client_message_id"`
	Sequence        int    `gorm:"column:sequence"`
	Role            string `gorm:"column:role"`
	Content         string `gorm:"column:content"`
	Status          string `gorm:"column:status"`
	ErrorCode       string `gorm:"column:error_code"`
	ErrorMessage    string `gorm:"column:error_message"`
	CreatedAt       string `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt       string `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (messageRow) TableName() string { return "messages" }

func messageToRow(record conversation.Message) messageRow {
	return messageRow{
		ID: record.ID, ConversationID: record.ConversationID, ClientMessageID: record.ClientMessageID,
		Sequence: record.Sequence, Role: string(record.Role), Content: record.Content,
		Status: string(record.Status), ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage,
		CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
	}
}

func (row messageRow) toMessage() (conversation.Message, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return conversation.Message{}, fmt.Errorf("parse message created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return conversation.Message{}, fmt.Errorf("parse message updated_at: %w", err)
	}
	return conversation.Message{
		ID: row.ID, ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		Sequence: row.Sequence, Role: conversation.MessageRole(row.Role), Content: row.Content,
		Status: conversation.MessageStatus(row.Status), ErrorCode: row.ErrorCode, ErrorMessage: row.ErrorMessage,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}
