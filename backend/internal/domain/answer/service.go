package answer

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
	Create(context.Context, string, CreateInput) (Attempt, error)
	Update(context.Context, string, UpdateInput) (Attempt, error)
	List(context.Context, string) ([]Attempt, error)
	Delete(context.Context, string) error
}

// Store is implemented by persistence adapters for answer attempts.
type Store interface {
	Create(context.Context, Attempt) error
	GetByID(context.Context, string) (Attempt, error)
	Update(context.Context, Attempt) error
	ListByQuestion(context.Context, string) ([]Attempt, error)
	Delete(context.Context, string) error
}

// QuestionChecker prevents answers from being created for missing questions
// without importing the question package.
type QuestionChecker interface {
	Exists(context.Context, string) error
}

type Dependencies struct {
	Store     Store
	Questions QuestionChecker
}

type service struct {
	store     Store
	questions QuestionChecker
}

func NewService(deps Dependencies) Service {
	return service{store: deps.Store, questions: deps.Questions}
}

func (s service) Create(ctx context.Context, questionID string, input CreateInput) (Attempt, error) {
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return Attempt{}, domainerr.ErrInvalidInput
	}
	if s.store == nil || s.questions == nil {
		return Attempt{}, errors.New("answer service: dependencies are not configured")
	}
	if err := validate(input.Result, input.DurationMs); err != nil {
		return Attempt{}, err
	}
	if err := s.questions.Exists(ctx, questionID); err != nil {
		return Attempt{}, err
	}
	id, err := newID()
	if err != nil {
		return Attempt{}, err
	}
	now := time.Now().UTC()
	attempt := Attempt{ID: id, QuestionID: questionID, BodyMarkdown: input.BodyMarkdown, Code: input.Code, CodeLanguage: input.CodeLanguage, Result: input.Result, DurationMs: input.DurationMs, CreatedAt: now, UpdatedAt: now}
	if err := s.store.Create(ctx, attempt); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (s service) Update(ctx context.Context, id string, input UpdateInput) (Attempt, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Attempt{}, domainerr.ErrInvalidInput
	}
	if s.store == nil {
		return Attempt{}, errors.New("answer service: store is not configured")
	}
	attempt, err := s.store.GetByID(ctx, id)
	if err != nil {
		return Attempt{}, err
	}
	if input.BodyMarkdown != nil {
		attempt.BodyMarkdown = *input.BodyMarkdown
	}
	if input.Code != nil {
		attempt.Code = *input.Code
	}
	if input.CodeLanguage != nil {
		attempt.CodeLanguage = *input.CodeLanguage
	}
	if input.Result != nil {
		attempt.Result = *input.Result
	}
	if input.DurationMs != nil {
		attempt.DurationMs = *input.DurationMs
	}
	if err := validate(attempt.Result, attempt.DurationMs); err != nil {
		return Attempt{}, err
	}
	attempt.UpdatedAt = time.Now().UTC()
	if err := s.store.Update(ctx, attempt); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (s service) List(ctx context.Context, questionID string) ([]Attempt, error) {
	questionID = strings.TrimSpace(questionID)
	if questionID == "" {
		return nil, domainerr.ErrInvalidInput
	}
	if s.store == nil || s.questions == nil {
		return nil, errors.New("answer service: dependencies are not configured")
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
		return errors.New("answer service: store is not configured")
	}
	return s.store.Delete(ctx, id)
}

func validate(result Result, duration *int64) error {
	switch result {
	case ResultSkipped, ResultIncorrect, ResultPartial, ResultCorrect:
	default:
		return domainerr.ErrInvalidInput
	}
	if duration != nil && *duration < 0 {
		return domainerr.ErrInvalidInput
	}
	return nil
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "a_" + hex.EncodeToString(bytes[:]), nil
}
