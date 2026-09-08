package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

type Service interface {
	Create(context.Context, string, CreateInput) (Record, error)
	Update(context.Context, string, UpdateInput) (Record, error)
	List(context.Context, string) ([]Record, error)
	Delete(context.Context, string) error
}

type Store interface {
	Create(context.Context, Record) error
	GetByID(context.Context, string) (Record, error)
	Update(context.Context, Record) error
	ListByQuestion(context.Context, string) ([]Record, error)
	Delete(context.Context, string) error
}

type QuestionChecker interface {
	Exists(context.Context, string) error
}

type AnswerReader interface {
	GetByID(context.Context, string) (answer.Attempt, error)
}

type Dependencies struct {
	Store     Store
	Answers   AnswerReader
	Questions QuestionChecker
}

type service struct {
	store     Store
	answers   AnswerReader
	questions QuestionChecker
}

func NewService(deps Dependencies) Service {
	return service{store: deps.Store, answers: deps.Answers, questions: deps.Questions}
}

func (s service) Create(ctx context.Context, questionID string, input CreateInput) (Record, error) {
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return Record{}, domainerr.ErrInvalidInput
	}
	if s.store == nil || s.questions == nil {
		return Record{}, errors.New("review service: dependencies are not configured")
	}
	if err := s.questions.Exists(ctx, questionID); err != nil {
		return Record{}, err
	}
	if err := s.validateAnswerAssociation(ctx, questionID, input.AnswerAttemptID); err != nil {
		return Record{}, err
	}
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	record := Record{ID: id, QuestionID: questionID, AnswerAttemptID: input.AnswerAttemptID, MistakeCategory: input.MistakeCategory, ReviewMarkdown: input.ReviewMarkdown, CorrectionMarkdown: input.CorrectionMarkdown, KeyConclusions: input.KeyConclusions, AIContentMarkdown: input.AIContentMarkdown, AISource: input.AISource, CreatedAt: now, UpdatedAt: now}
	if err := s.store.Create(ctx, record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s service) Update(ctx context.Context, id string, input UpdateInput) (Record, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Record{}, domainerr.ErrInvalidInput
	}
	if s.store == nil {
		return Record{}, errors.New("review service: store is not configured")
	}
	record, err := s.store.GetByID(ctx, id)
	if err != nil {
		return Record{}, err
	}
	if input.AnswerAttemptID != nil {
		record.AnswerAttemptID = *input.AnswerAttemptID
	}
	if input.MistakeCategory != nil {
		record.MistakeCategory = *input.MistakeCategory
	}
	if input.ReviewMarkdown != nil {
		record.ReviewMarkdown = *input.ReviewMarkdown
	}
	if input.CorrectionMarkdown != nil {
		record.CorrectionMarkdown = *input.CorrectionMarkdown
	}
	if input.KeyConclusions != nil {
		record.KeyConclusions = *input.KeyConclusions
	}
	if input.AIContentMarkdown != nil {
		record.AIContentMarkdown = *input.AIContentMarkdown
	}
	if input.AISource != nil {
		record.AISource = *input.AISource
	}
	if err := s.validateAnswerAssociation(ctx, record.QuestionID, record.AnswerAttemptID); err != nil {
		return Record{}, err
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, record); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s service) List(ctx context.Context, questionID string) ([]Record, error) {
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return nil, domainerr.ErrInvalidInput
	}
	if s.store == nil || s.questions == nil {
		return nil, errors.New("review service: dependencies are not configured")
	}
	if err := s.questions.Exists(ctx, questionID); err != nil {
		return nil, err
	}
	return s.store.ListByQuestion(ctx, questionID)
}

func (s service) Delete(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return domainerr.ErrInvalidInput
	}
	if s.store == nil {
		return errors.New("review service: store is not configured")
	}
	return s.store.Delete(ctx, id)
}

func (s service) validateAnswerAssociation(ctx context.Context, questionID string, answerID *string) error {
	if answerID == nil {
		return nil
	}
	if s.answers == nil {
		return errors.New("review service: answer reader is not configured")
	}
	attempt, err := s.answers.GetByID(ctx, *answerID)
	if err != nil {
		return err
	}
	if attempt.QuestionID != questionID {
		return domainerr.ErrInvalidInput
	}
	return nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "r_" + hex.EncodeToString(bytes[:]), nil
}
