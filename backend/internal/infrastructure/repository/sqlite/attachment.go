package sqlite

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/repository"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// AttachmentRepository stores metadata only; the service manages physical files.
type AttachmentRepository struct {
	db *storage.DB
}

func NewAttachmentRepository(db *storage.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
}

func (r *AttachmentRepository) Create(ctx context.Context, record question.Attachment) error {
	row := attachmentToRow(record)
	if err := r.db.ORM(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert attachment: %w", err)
	}
	return nil
}

func (r *AttachmentRepository) GetByID(ctx context.Context, id string) (question.Attachment, error) {
	var row attachmentRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return question.Attachment{}, question.ErrNotFound
		}
		return question.Attachment{}, fmt.Errorf("get attachment by ID: %w", err)
	}
	return row.record()
}

func (r *AttachmentRepository) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]question.Attachment, error) {
	var rows []attachmentRow
	if err := r.db.ORM(ctx).Where("owner_type = ? AND owner_id = ?", ownerType, ownerID).
		Order("created_at DESC").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query attachments by owner: %w", err)
	}
	list := make([]question.Attachment, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		list = append(list, record)
	}
	return list, nil
}

func (r *AttachmentRepository) Delete(ctx context.Context, id string) error {
	return requireORMAffected(r.db.ORM(ctx).Where("id = ?", id).Delete(&attachmentRow{}), "delete attachment")
}

var _ repository.AttachmentRepository = (*AttachmentRepository)(nil)
