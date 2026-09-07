package sqlite

import (
	"context"

	"interview-memory-agent/backend/internal/question"
	"interview-memory-agent/backend/internal/storage"
)

// ReviewRepository stores mistake reviews and optional answer associations.
type ReviewRepository struct {
	db *storage.DB
}

func NewReviewRepository(db *storage.DB) *ReviewRepository {
	return &ReviewRepository{db: db}
}

func (r *ReviewRepository) Create(ctx context.Context, record question.MistakeReview) error {
	// TODO: Insert the review with a nullable answer_attempt_id.
	// The service must verify that a linked answer belongs to the same question.
	return question.ErrNotImplemented
}

func (r *ReviewRepository) GetByID(ctx context.Context, id string) (question.MistakeReview, error) {
	// TODO: Load the review; map sql.ErrNoRows to question.ErrNotFound.
	return question.MistakeReview{}, question.ErrNotImplemented
}

func (r *ReviewRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.MistakeReview, error) {
	// TODO: List reviews by created_at DESC, id DESC; return an empty slice when absent.
	return nil, question.ErrNotImplemented
}

func (r *ReviewRepository) Update(ctx context.Context, record question.MistakeReview) error {
	// TODO: Update editable fields and updated_at; check affected rows.
	return question.ErrNotImplemented
}

func (r *ReviewRepository) Delete(ctx context.Context, id string) error {
	// TODO: Delete the review and its attachment metadata atomically; check affected rows.
	return question.ErrNotImplemented
}

var _ question.ReviewRepository = (*ReviewRepository)(nil)
