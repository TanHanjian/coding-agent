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

// AnswerRepository stores answer attempts associated with questions.
type AnswerRepository struct {
	db *storage.DB
}

func NewAnswerRepository(db *storage.DB) *AnswerRepository {
	return &AnswerRepository{db: db}
}

func (r *AnswerRepository) Create(ctx context.Context, record question.AnswerAttempt) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO answer_attempts (
			id, question_id, body_markdown, code, code_language,
			result, duration_ms, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.QuestionID,
		record.BodyMarkdown,
		record.Code,
		record.CodeLanguage,
		record.Result,
		record.DurationMs,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert answer attempt: %w", err)
	}
	return nil
}

func (r *AnswerRepository) GetByID(ctx context.Context, id string) (question.AnswerAttempt, error) {
	// TODO: Load the answer; map sql.ErrNoRows to question.ErrNotFound.
	result := r.db.QueryRowContext(ctx, `
		SELECT id, question_id, body_markdown, code, code_language,
		result, duration_ms, created_at, updated_at
		FROM answer_attempts
		WHERE id = ?`, id,
	)
	var record question.AnswerAttempt
	var createdAtStr, updatedAtStr string
	err := result.Scan(
		&record.ID,
		&record.QuestionID,
		&record.BodyMarkdown,
		&record.Code,
		&record.CodeLanguage,
		&record.Result,
		&record.DurationMs,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return question.AnswerAttempt{}, question.ErrNotFound
		}
		return question.AnswerAttempt{}, fmt.Errorf("scan answer attempt: %w", err)
	}
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return question.AnswerAttempt{}, fmt.Errorf("parse created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return question.AnswerAttempt{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return record, nil
}

func (r *AnswerRepository) Update(ctx context.Context, record question.AnswerAttempt) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE answer_attempts
		SET body_markdown = ?, code = ?, code_language = ?,
			result = ?, duration_ms = ?, updated_at = ?
		WHERE id = ?`,
		record.BodyMarkdown,
		record.Code,
		record.CodeLanguage,
		record.Result,
		record.DurationMs,
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		record.ID,
	)
	if err != nil {
		return fmt.Errorf("update answer attempt: %w", err)
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

func (r *AnswerRepository) ListByQuestion(ctx context.Context, questionID string) ([]question.AnswerAttempt, error) {
	// TODO: List answers by created_at DESC, id DESC; return an empty slice when absent.
	result, err := r.db.QueryContext(ctx, `
		SELECT id, question_id, body_markdown, code, code_language,
		       result, duration_ms, created_at, updated_at
		FROM answer_attempts
		WHERE question_id = ?
		ORDER BY created_at DESC, id DESC`, questionID,
	)
	if err != nil {
		return nil, fmt.Errorf("query answer attempts: %w", err)
	}
	defer result.Close()

	attempts := make([]question.AnswerAttempt, 0)
	for result.Next() {
		var record question.AnswerAttempt
		var createdAtStr, updatedAtStr string
		err := result.Scan(
			&record.ID,
			&record.QuestionID,
			&record.BodyMarkdown,
			&record.Code,
			&record.CodeLanguage,
			&record.Result,
			&record.DurationMs,
			&createdAtStr,
			&updatedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("scan answer attempt: %w", err)
		}
		record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		attempts = append(attempts, record)
	}

	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("iterate answer attempts: %w", err)
	}
	return attempts, nil
}

func (r *AnswerRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM answer_attempts WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete answer attempt: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check deleted answer attempt: %w", err)
		}
		if affected == 0 {
			return question.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE owner_type = 'answer' AND owner_id = ?`, id); err != nil {
			return fmt.Errorf("delete answer attachments: %w", err)
		}
		return nil
	})
}

var _ question.AnswerRepository = (*AnswerRepository)(nil)
