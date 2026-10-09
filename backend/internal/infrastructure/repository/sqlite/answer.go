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

// AnswerRepository stores answer attempts associated with questions.
type AnswerRepository struct {
	db *storage.DB
}

func NewAnswerRepository(db *storage.DB) *AnswerRepository {
	return &AnswerRepository{db: db}
}

func (r *AnswerRepository) Create(ctx context.Context, record question.AnswerAttempt) error {
	row := answerToRow(record)
	if err := r.db.ORM(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert answer attempt: %w", err)
	}
	return nil
}

func (r *AnswerRepository) GetByID(ctx context.Context, id string) (question.AnswerAttempt, error) {
	var row answerRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return question.AnswerAttempt{}, question.ErrNotFound
		}
		return question.AnswerAttempt{}, fmt.Errorf("scan answer attempt: %w", err)
	}
	return row.record()
}

func (r *AnswerRepository) Update(ctx context.Context, record question.AnswerAttempt) error {
	result := r.db.ORM(ctx).Model(&answerRow{}).Where("id = ?", record.ID).Updates(map[string]any{
		"body_markdown": record.BodyMarkdown, "code": record.Code, "code_language": record.CodeLanguage,
		"result": string(record.Result), "duration_ms": record.DurationMs, "updated_at": formatTime(record.UpdatedAt),
	})
	return requireORMAffected(result, "update answer attempt")
}

func (r *AnswerRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.AnswerAttempt, error) {
	var rows []answerRow
	if err := r.db.ORM(ctx).Where("question_id = ?", questionID).
		Order("created_at DESC").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query answer attempts: %w", err)
	}
	attempts := make([]question.AnswerAttempt, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, record)
	}
	return attempts, nil
}

func (r *AnswerRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		if err := requireORMAffected(tx.Where("id = ?", id).Delete(&answerRow{}), "delete answer attempt"); err != nil {
			return err
		}
		if err := tx.Where("owner_type = ? AND owner_id = ?", "answer", id).Delete(&attachmentRow{}).Error; err != nil {
			return fmt.Errorf("delete answer attachments: %w", err)
		}
		return nil
	})
}

var _ repository.AnswerRepository = (*AnswerRepository)(nil)
