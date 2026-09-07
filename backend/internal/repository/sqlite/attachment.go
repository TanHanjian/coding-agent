package sqlite

import (
	"context"

	"interview-memory-agent/backend/internal/question"
	"interview-memory-agent/backend/internal/storage"
)

// AttachmentRepository stores metadata only; the service manages physical files.
type AttachmentRepository struct {
	db *storage.DB
}

func NewAttachmentRepository(db *storage.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
}

func (r *AttachmentRepository) Create(ctx context.Context, record question.Attachment) error {
	// TODO: Insert metadata after the service validates the owner and uploaded file.
	return question.ErrNotImplemented
}

func (r *AttachmentRepository) GetByID(ctx context.Context, id string) (question.Attachment, error) {
	// TODO: Load metadata; map sql.ErrNoRows to question.ErrNotFound.
	return question.Attachment{}, question.ErrNotImplemented
}

func (r *AttachmentRepository) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]question.Attachment, error) {
	// TODO: Filter by both owner fields; return an empty slice when absent.
	return nil, question.ErrNotImplemented
}

func (r *AttachmentRepository) Delete(ctx context.Context, id string) error {
	// TODO: Delete metadata and check affected rows; coordinate file cleanup in the service.
	return question.ErrNotImplemented
}

var _ question.AttachmentRepository = (*AttachmentRepository)(nil)
