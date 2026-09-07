package sqlite

import (
	"context"

	"interview-memory-agent/backend/internal/question"
	"interview-memory-agent/backend/internal/storage"
)

// AnswerRepository stores answer attempts associated with questions.
type AnswerRepository struct {
	db *storage.DB
}

func NewAnswerRepository(db *storage.DB) *AnswerRepository {
	return &AnswerRepository{db: db}
}

func (r *AnswerRepository) Create(ctx context.Context, record question.AnswerAttempt) error {
	// TODO: Insert the answer with its question ID and service-supplied timestamps.
	return question.ErrNotImplemented
}

func (r *AnswerRepository) GetByID(ctx context.Context, id string) (question.AnswerAttempt, error) {
	// TODO: Load the answer; map sql.ErrNoRows to question.ErrNotFound.
	return question.AnswerAttempt{}, question.ErrNotImplemented
}

func (r *AnswerRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.AnswerAttempt, error) {
	// TODO: List answers by created_at DESC, id DESC; return an empty slice when absent.
	return nil, question.ErrNotImplemented
}

func (r *AnswerRepository) Update(ctx context.Context, record question.AnswerAttempt) error {
	// TODO: Update editable fields and updated_at; check affected rows.
	return question.ErrNotImplemented
}

func (r *AnswerRepository) Delete(ctx context.Context, id string) error {
	// TODO: Delete the answer and its attachment metadata atomically; check affected rows.
	// Linked reviews retain their content via ON DELETE SET NULL.
	return question.ErrNotImplemented
}

var _ question.AnswerRepository = (*AnswerRepository)(nil)
