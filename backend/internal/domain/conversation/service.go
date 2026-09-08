package conversation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

type Service interface {
	CreateConversation(context.Context, CreateConversationInput) (Conversation, error)
	GetConversation(context.Context, string) (Conversation, error)
	ListConversations(context.Context) ([]Conversation, error)
	UpdateConversation(context.Context, string, UpdateConversationInput) (Conversation, error)
	DeleteConversation(context.Context, string) error
	CreateMessage(context.Context, string, CreateMessageInput) (Message, error)
	GetMessage(context.Context, string) (Message, error)
	ListMessages(context.Context, string) ([]Message, error)
	UpdateAssistantMessage(context.Context, string, UpdateAssistantMessageInput) (Message, error)
	DeleteMessage(context.Context, string) error
}

type ConversationStore interface {
	Create(context.Context, Conversation) error
	GetByID(context.Context, string) (Conversation, error)
	List(context.Context) ([]Conversation, error)
	UpdateTitle(context.Context, string, string, time.Time) error
	Delete(context.Context, string) error
}

type MessageStore interface {
	Create(context.Context, Message) (Message, error)
	GetByID(context.Context, string) (Message, error)
	ListByConversation(context.Context, string) ([]Message, error)
	UpdateAssistant(context.Context, Message) error
	Delete(context.Context, string) error
}

type Dependencies struct {
	Conversations ConversationStore
	Messages      MessageStore
}

type service struct {
	conversations ConversationStore
	messages      MessageStore
}

func NewService(deps Dependencies) Service {
	return service{conversations: deps.Conversations, messages: deps.Messages}
}

func (s service) CreateConversation(ctx context.Context, input CreateConversationInput) (Conversation, error) {
	if s.conversations == nil {
		return Conversation{}, errors.New("conversation service: conversation store is not configured")
	}
	id, err := newID("c")
	if err != nil {
		return Conversation{}, err
	}
	now := time.Now().UTC()
	record := Conversation{ID: id, Title: strings.TrimSpace(input.Title), CreatedAt: now, UpdatedAt: now}
	if err := s.conversations.Create(ctx, record); err != nil {
		return Conversation{}, err
	}
	return record, nil
}

func (s service) GetConversation(ctx context.Context, id string) (Conversation, error) {
	if err := requireID(id); err != nil {
		return Conversation{}, err
	}
	if s.conversations == nil {
		return Conversation{}, errors.New("conversation service: conversation store is not configured")
	}
	return s.conversations.GetByID(ctx, strings.TrimSpace(id))
}

func (s service) ListConversations(ctx context.Context) ([]Conversation, error) {
	if s.conversations == nil {
		return nil, errors.New("conversation service: conversation store is not configured")
	}
	return s.conversations.List(ctx)
}

func (s service) UpdateConversation(ctx context.Context, id string, input UpdateConversationInput) (Conversation, error) {
	if err := requireID(id); err != nil || input.Title == nil {
		return Conversation{}, domainerr.ErrInvalidInput
	}
	if s.conversations == nil {
		return Conversation{}, errors.New("conversation service: conversation store is not configured")
	}
	record, err := s.conversations.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return Conversation{}, err
	}
	record.Title = strings.TrimSpace(*input.Title)
	record.UpdatedAt = time.Now().UTC()
	if err := s.conversations.UpdateTitle(ctx, record.ID, record.Title, record.UpdatedAt); err != nil {
		return Conversation{}, err
	}
	return record, nil
}

func (s service) DeleteConversation(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	if s.conversations == nil {
		return errors.New("conversation service: conversation store is not configured")
	}
	return s.conversations.Delete(ctx, strings.TrimSpace(id))
}

func (s service) CreateMessage(ctx context.Context, conversationID string, input CreateMessageInput) (Message, error) {
	if err := requireID(conversationID); err != nil {
		return Message{}, err
	}
	if s.messages == nil {
		return Message{}, errors.New("conversation service: message store is not configured")
	}
	if input.Role != MessageRoleUser && input.Role != MessageRoleAssistant {
		return Message{}, domainerr.ErrInvalidInput
	}
	if input.Role == MessageRoleUser && strings.TrimSpace(input.Content) == "" {
		return Message{}, domainerr.ErrInvalidInput
	}
	id, err := newID("m")
	if err != nil {
		return Message{}, err
	}
	now := time.Now().UTC()
	status := MessageStatusCompleted
	content := input.Content
	if input.Role == MessageRoleAssistant {
		status = MessageStatusStreaming
		content = ""
	}
	return s.messages.Create(ctx, Message{ID: id, ConversationID: strings.TrimSpace(conversationID), Role: input.Role, Content: content, Status: status, CreatedAt: now, UpdatedAt: now})
}

func (s service) GetMessage(ctx context.Context, id string) (Message, error) {
	if err := requireID(id); err != nil {
		return Message{}, err
	}
	if s.messages == nil {
		return Message{}, errors.New("conversation service: message store is not configured")
	}
	return s.messages.GetByID(ctx, strings.TrimSpace(id))
}

func (s service) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	if err := requireID(conversationID); err != nil {
		return nil, err
	}
	if s.conversations == nil || s.messages == nil {
		return nil, errors.New("conversation service: stores are not configured")
	}
	if _, err := s.conversations.GetByID(ctx, strings.TrimSpace(conversationID)); err != nil {
		return nil, err
	}
	return s.messages.ListByConversation(ctx, strings.TrimSpace(conversationID))
}

func (s service) UpdateAssistantMessage(ctx context.Context, id string, input UpdateAssistantMessageInput) (Message, error) {
	if err := requireID(id); err != nil || input.Content == nil || !isTerminalStatus(input.Status) {
		return Message{}, domainerr.ErrInvalidInput
	}
	if input.Status != MessageStatusFailed && (input.ErrorCode != "" || input.ErrorMessage != "") {
		return Message{}, domainerr.ErrInvalidInput
	}
	if s.messages == nil {
		return Message{}, errors.New("conversation service: message store is not configured")
	}
	record, err := s.messages.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return Message{}, err
	}
	if record.Role != MessageRoleAssistant || record.Status != MessageStatusStreaming {
		return Message{}, domainerr.ErrInvalidInput
	}
	record.Content = *input.Content
	record.Status = input.Status
	record.ErrorCode = input.ErrorCode
	record.ErrorMessage = input.ErrorMessage
	record.UpdatedAt = time.Now().UTC()
	if err := s.messages.UpdateAssistant(ctx, record); err != nil {
		return Message{}, err
	}
	return record, nil
}

func (s service) DeleteMessage(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	if s.messages == nil {
		return errors.New("conversation service: message store is not configured")
	}
	return s.messages.Delete(ctx, strings.TrimSpace(id))
}

func requireID(id string) error {
	if strings.TrimSpace(id) == "" {
		return domainerr.ErrInvalidInput
	}
	return nil
}

func isTerminalStatus(status MessageStatus) bool {
	return status == MessageStatusCompleted || status == MessageStatusCancelled || status == MessageStatusFailed
}

func newID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}
