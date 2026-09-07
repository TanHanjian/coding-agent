package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/question"
	"interview-memory-agent/backend/internal/storage"
)

// ReviewRepository stores mistake reviews and optional answer associations.
type ReviewRepository struct{ db *storage.DB }

func NewReviewRepository(db *storage.DB) *ReviewRepository { return &ReviewRepository{db: db} }

func (r *ReviewRepository) Create(ctx context.Context, record question.MistakeReview) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := validateReviewAnswer(ctx, tx, record.QuestionID, record.AnswerAttemptID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO mistake_reviews (
 id, question_id, answer_attempt_id, mistake_category, review_markdown,
 correction_markdown, key_conclusions, ai_content_markdown, ai_source, created_at, updated_at
 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.QuestionID, record.AnswerAttemptID, record.MistakeCategory,
			record.ReviewMarkdown, record.CorrectionMarkdown, record.KeyConclusions,
			record.AIContentMarkdown, record.AISource,
			record.CreatedAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("insert mistake review: %w", err)
		}
		return nil
	})
}

// Validate inside the write transaction so the association cannot change between checks.
func validateReviewAnswer(ctx context.Context, tx *sql.Tx, questionID string, answerID *string) error {
	if answerID == nil {
		return nil
	}
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM answer_attempts WHERE id = ? AND question_id = ?)`, *answerID, questionID).Scan(&valid); err != nil {
		return fmt.Errorf("validate review answer: %w", err)
	}
	if !valid {
		return question.ErrInvalidInput
	}
	return nil
}

const reviewColumns = `id, question_id, answer_attempt_id, mistake_category, review_markdown,
 correction_markdown, key_conclusions, ai_content_markdown, ai_source, created_at, updated_at`

// Both single-row and list queries use the same column order and time parsing.
func scanReview(row interface{ Scan(...any) error }) (question.MistakeReview, error) {
	var record question.MistakeReview
	var createdAt, updatedAt string
	err := row.Scan(&record.ID, &record.QuestionID, &record.AnswerAttemptID,
		&record.MistakeCategory, &record.ReviewMarkdown, &record.CorrectionMarkdown,
		&record.KeyConclusions, &record.AIContentMarkdown, &record.AISource, &createdAt, &updatedAt)
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("scan mistake review: %w", err)
	}
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("parse review created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("parse review updated_at: %w", err)
	}
	return record, nil
}

func (r *ReviewRepository) GetByID(ctx context.Context, id string) (question.MistakeReview, error) {
	record, err := scanReview(r.db.QueryRowContext(ctx, `SELECT `+reviewColumns+` FROM mistake_reviews WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return question.MistakeReview{}, question.ErrNotFound
	}
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("get mistake review by ID: %w", err)
	}
	return record, nil
}

func (r *ReviewRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.MistakeReview, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+reviewColumns+` FROM mistake_reviews WHERE question_id = ? ORDER BY created_at DESC, id DESC`, questionID)
	if err != nil {
		return nil, fmt.Errorf("query mistake reviews: %w", err)
	}
	defer rows.Close()
	records := make([]question.MistakeReview, 0)
	for rows.Next() {
		record, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mistake reviews: %w", err)
	}
	return records, nil
}

func (r *ReviewRepository) Update(ctx context.Context, record question.MistakeReview) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var questionID string
		if err := tx.QueryRowContext(ctx, `SELECT question_id FROM mistake_reviews WHERE id = ?`, record.ID).Scan(&questionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return question.ErrNotFound
			}
			return fmt.Errorf("get review question: %w", err)
		}
		if err := validateReviewAnswer(ctx, tx, questionID, record.AnswerAttemptID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE mistake_reviews SET
 answer_attempt_id = ?, mistake_category = ?, review_markdown = ?, correction_markdown = ?,
 key_conclusions = ?, ai_content_markdown = ?, ai_source = ?, updated_at = ? WHERE id = ?`,
			record.AnswerAttemptID, record.MistakeCategory, record.ReviewMarkdown, record.CorrectionMarkdown,
			record.KeyConclusions, record.AIContentMarkdown, record.AISource,
			record.UpdatedAt.UTC().Format(time.RFC3339Nano), record.ID)
		if err != nil {
			return fmt.Errorf("update mistake review: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check updated mistake review: %w", err)
		}
		if affected == 0 {
			return question.ErrNotFound
		}
		return nil
	})
}

func (r *ReviewRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM mistake_reviews WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete mistake review: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check deleted mistake review: %w", err)
		}
		if affected == 0 {
			return question.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE owner_type = 'review' AND owner_id = ?`, id); err != nil {
			return fmt.Errorf("delete review attachments: %w", err)
		}
		return nil
	})
}

var _ question.ReviewRepository = (*ReviewRepository)(nil)
