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

// QuestionRepository stores question records in SQLite.
type QuestionRepository struct {
	db *storage.DB
}

func NewQuestionRepository(db *storage.DB) *QuestionRepository {
	return &QuestionRepository{db: db}
}

// Create writes the question and its tags atomically. Validation, IDs and
// timestamps are supplied by the service layer.
func (r *QuestionRepository) Create(ctx context.Context, record question.QuestionRecord) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var difficulty any
		if record.Difficulty != nil {
			difficulty = string(*record.Difficulty)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO questions (
				id, title, type, body_markdown, difficulty, source_name,
				source_url, is_archived, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			record.ID, record.Title, record.Type, record.BodyMarkdown, difficulty,
			record.SourceName, record.SourceURL, record.IsArchived,
			record.CreatedAt.UTC().Format(time.RFC3339Nano),
			record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return fmt.Errorf("insert question: %w", err)
		}
		for _, tag := range record.Tags {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO question_tags (question_id, tag) VALUES (?, ?)`,
				record.ID, tag,
			); err != nil {
				return fmt.Errorf("insert question tag: %w", err)
			}
		}
		return nil
	})
}

func (r *QuestionRepository) GetByID(ctx context.Context, id string) (question.QuestionRecord, error) {
	var record question.QuestionRecord
	var createdAt, updatedAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, type, body_markdown, difficulty, source_name, source_url, is_archived, created_at, updated_at
		FROM questions
		WHERE id = ?
	`, id).Scan(
		&record.ID,
		&record.Title,
		&record.Type,
		&record.BodyMarkdown,
		&record.Difficulty,
		&record.SourceName,
		&record.SourceURL,
		&record.IsArchived,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return question.QuestionRecord{}, question.ErrNotFound
		}
		return question.QuestionRecord{}, fmt.Errorf("get question by ID: %w", err)
	}
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return question.QuestionRecord{}, fmt.Errorf("parse question created_at: %w", err)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return question.QuestionRecord{}, fmt.Errorf("parse question updated_at: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
	SELECT tag
	FROM question_tags
	WHERE question_id = ?
	ORDER BY tag
`, id)
	if err != nil {
		return question.QuestionRecord{}, fmt.Errorf("query question tags: %w", err)
	}
	defer rows.Close()

	record.Tags = make([]string, 0)
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return question.QuestionRecord{}, fmt.Errorf("scan question tag: %w", err)
		}
		record.Tags = append(record.Tags, tag)
	}
	if err := rows.Err(); err != nil {
		return question.QuestionRecord{}, fmt.Errorf("iterate question tags: %w", err)
	}
	return record, nil
}

func (r *QuestionRepository) Update(ctx context.Context, record question.QuestionRecord) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var difficulty any
		if record.Difficulty != nil {
			difficulty = string(*record.Difficulty)
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE questions
			SET title = ?, type = ?, body_markdown = ?, difficulty = ?, source_name = ?, source_url = ?, is_archived = ?, updated_at = ?
			WHERE id = ?
		`, record.Title, record.Type, record.BodyMarkdown, difficulty, record.SourceName, record.SourceURL, record.IsArchived,
			record.UpdatedAt.UTC().Format(time.RFC3339Nano), record.ID)
		if err != nil {
			return fmt.Errorf("update question: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check updated question: %w", err)
		}
		if affected == 0 {
			return question.ErrNotFound
		}
		// A nil slice means the caller did not request a tag update.
		if record.Tags == nil {
			return nil
		}
		// A non-nil slice replaces the entire tag set; an empty slice clears it.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM question_tags WHERE question_id = ?`, record.ID,
		); err != nil {
			return fmt.Errorf("delete question tags: %w", err)
		}
		for _, tag := range record.Tags {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO question_tags (question_id, tag) VALUES (?, ?)`,
				record.ID, tag,
			); err != nil {
				return fmt.Errorf("insert question tag: %w", err)
			}
		}
		return nil
	})
}

func (r *QuestionRepository) SetArchived(ctx context.Context, id string, isArchived bool) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE questions
		SET is_archived = ?, updated_at = ?
		WHERE id = ?
	`, isArchived, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("set question archived: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check updated question: %w", err)
	}
	if affected == 0 {
		return question.ErrNotFound
	}
	return nil
}

func (r *QuestionRepository) DeletePermanent(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM question_tags WHERE question_id = ?`, id); err != nil {
			return fmt.Errorf("delete question tags: %w", err)
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM questions WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete question: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("check deleted question: %w", err)
		}
		if affected == 0 {
			return question.ErrNotFound
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM answer_attempts WHERE question_id = ?`, id); err != nil {
			return fmt.Errorf("delete answer attempts: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM mistake_reviews WHERE question_id = ?`, id); err != nil {
			return fmt.Errorf("delete mistake reviews: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE owner_type = 'question' AND owner_id = ?`, id); err != nil {
			return fmt.Errorf("delete attachments: %w", err)
		}
		return nil
	})
}

func (r *QuestionRepository) Search(ctx context.Context, query question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	// Implementation of search functionality is omitted for brevity.
	return question.QuestionSearchResult{}, nil
}

func (r *QuestionRepository) GetDetail(ctx context.Context, id string) (question.QuestionDetail, error) {
	// Implementation of get detail functionality is omitted for brevity.
	return question.QuestionDetail{}, nil
}

var _ question.QuestionRepository = (*QuestionRepository)(nil)
