package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO attachments (
			id, question_id, owner_type, owner_id, original_name,
			stored_name, mime_type, size_bytes, sha256, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.QuestionID,
		record.OwnerType,
		record.OwnerID,
		record.OriginalName,
		record.StoredName,
		record.MIMEType,
		record.SizeBytes,
		record.SHA256,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert attachment: %w", err)
	}
	return nil
}

func (r *AttachmentRepository) GetByID(ctx context.Context, id string) (question.Attachment, error) {
	var record question.Attachment
	row := r.db.QueryRowContext(ctx, `
		SELECT id, question_id, owner_type, owner_id, original_name,
		stored_name, mime_type, size_bytes, sha256, created_at
		FROM attachments
		WHERE id = ?`, id,
	)
	var createdAtStr string
	err := row.Scan(
		&record.ID,
		&record.QuestionID,
		&record.OwnerType,
		&record.OwnerID,
		&record.OriginalName,
		&record.StoredName,
		&record.MIMEType,
		&record.SizeBytes,
		&record.SHA256,
		&createdAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return question.Attachment{}, question.ErrNotFound
		}
		return question.Attachment{}, fmt.Errorf("get attachment by ID: %w", err)
	}

	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return question.Attachment{}, fmt.Errorf("parse created_at: %w", err)
	}

	return record, nil
}

func (r *AttachmentRepository) ListByOwner(ctx context.Context, ownerType, ownerID string) ([]question.Attachment, error) {
	// TODO: Filter by both owner fields; return an empty slice when absent.
	list := []question.Attachment{}
	result, err := r.db.QueryContext(ctx, `
		SELECT id, question_id, owner_type, owner_id, original_name,
		       stored_name, mime_type, size_bytes, sha256, created_at
		FROM attachments
		WHERE owner_type = ? AND owner_id = ?
		ORDER BY created_at DESC, id DESC
	`, ownerType, ownerID)
	if err != nil {
		return nil, fmt.Errorf("query attachments by owner: %w", err)
	}
	defer result.Close()

	for result.Next() {
		var record question.Attachment
		var createdAtStr string

		err := result.Scan(
			&record.ID,
			&record.QuestionID,
			&record.OwnerType,
			&record.OwnerID,
			&record.OriginalName,
			&record.StoredName,
			&record.MIMEType,
			&record.SizeBytes,
			&record.SHA256,
			&createdAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}

		record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}

		list = append(list, record)
	}

	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("iterate attachments by owner: %w", err)
	}
	return list, nil
}

func (r *AttachmentRepository) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete attachment: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return question.ErrNotFound
	}

	return nil
}

var _ repository.AttachmentRepository = (*AttachmentRepository)(nil)
