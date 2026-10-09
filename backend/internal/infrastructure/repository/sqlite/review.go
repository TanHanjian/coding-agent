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

// ReviewRepository stores mistake reviews and optional answer associations.
type ReviewRepository struct{ db *storage.DB }

func NewReviewRepository(db *storage.DB) *ReviewRepository { return &ReviewRepository{db: db} }

func (r *ReviewRepository) Create(ctx context.Context, record question.MistakeReview) error {
	return r.db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		if err := validateReviewAnswer(tx, record.QuestionID, record.AnswerAttemptID); err != nil {
			return err
		}
		row := reviewToRow(record)
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("insert mistake review: %w", err)
		}
		return nil
	})
}

// Validate inside the write transaction so the association cannot change between checks.
func validateReviewAnswer(tx *gorm.DB, questionID string, answerID *string) error {
	if answerID == nil {
		return nil
	}
	var count int64
	if err := tx.Model(&answerRow{}).Where("id = ? AND question_id = ?", *answerID, questionID).Count(&count).Error; err != nil {
		return fmt.Errorf("validate review answer: %w", err)
	}
	if count == 0 {
		return question.ErrInvalidInput
	}
	return nil
}

func (r *ReviewRepository) GetByID(ctx context.Context, id string) (question.MistakeReview, error) {
	var row reviewRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return question.MistakeReview{}, question.ErrNotFound
		}
		return question.MistakeReview{}, fmt.Errorf("get mistake review by ID: %w", err)
	}
	record, err := row.record()
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("get mistake review by ID: %w", err)
	}
	return record, nil
}

func (r *ReviewRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.MistakeReview, error) {
	var rows []reviewRow
	if err := r.db.ORM(ctx).Where("question_id = ?", questionID).
		Order("created_at DESC").Order("id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("query mistake reviews: %w", err)
	}
	records := make([]question.MistakeReview, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

func (r *ReviewRepository) Update(ctx context.Context, record question.MistakeReview) error {
	return r.db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		var existing reviewRow
		if err := tx.Select("question_id").Where("id = ?", record.ID).Take(&existing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return question.ErrNotFound
			}
			return fmt.Errorf("get review question: %w", err)
		}
		if err := validateReviewAnswer(tx, existing.QuestionID, record.AnswerAttemptID); err != nil {
			return err
		}
		result := tx.Model(&reviewRow{}).Where("id = ?", record.ID).Updates(map[string]any{
			"answer_attempt_id": record.AnswerAttemptID, "mistake_category": record.MistakeCategory,
			"review_markdown": record.ReviewMarkdown, "correction_markdown": record.CorrectionMarkdown,
			"key_conclusions": record.KeyConclusions, "ai_content_markdown": record.AIContentMarkdown,
			"ai_source": record.AISource, "updated_at": formatTime(record.UpdatedAt),
		})
		return requireORMAffected(result, "update mistake review")
	})
}

func (r *ReviewRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(_ context.Context, tx *gorm.DB) error {
		if err := requireORMAffected(tx.Where("id = ?", id).Delete(&reviewRow{}), "delete mistake review"); err != nil {
			return err
		}
		if err := tx.Where("owner_type = ? AND owner_id = ?", "review", id).Delete(&attachmentRow{}).Error; err != nil {
			return fmt.Errorf("delete review attachments: %w", err)
		}
		return nil
	})
}

var _ repository.ReviewRepository = (*ReviewRepository)(nil)
