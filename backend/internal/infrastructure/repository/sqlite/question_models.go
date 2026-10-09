package sqlite

import (
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/question"
)

// questionRow mirrors the existing schema without ORM associations or clocks.
type questionRow struct {
	ID           string  `gorm:"column:id;primaryKey"`
	Title        string  `gorm:"column:title"`
	Type         string  `gorm:"column:type"`
	BodyMarkdown string  `gorm:"column:body_markdown"`
	Difficulty   *string `gorm:"column:difficulty"`
	SourceName   string  `gorm:"column:source_name"`
	SourceURL    string  `gorm:"column:source_url"`
	IsArchived   bool    `gorm:"column:is_archived"`
	CreatedAt    string  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    string  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (questionRow) TableName() string { return "questions" }

type questionTagRow struct {
	QuestionID string `gorm:"column:question_id;primaryKey"`
	Tag        string `gorm:"column:tag;primaryKey"`
}

func (questionTagRow) TableName() string { return "question_tags" }

func questionToRow(record question.QuestionRecord) questionRow {
	var difficulty *string
	if record.Difficulty != nil {
		value := string(*record.Difficulty)
		difficulty = &value
	}
	return questionRow{
		ID: record.ID, Title: record.Title, Type: string(record.Type),
		BodyMarkdown: record.BodyMarkdown, Difficulty: difficulty,
		SourceName: record.SourceName, SourceURL: record.SourceURL,
		IsArchived: record.IsArchived,
		CreatedAt:  formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
	}
}

func (row questionRow) record() (question.QuestionRecord, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return question.QuestionRecord{}, fmt.Errorf("parse question created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return question.QuestionRecord{}, fmt.Errorf("parse question updated_at: %w", err)
	}
	var difficulty *question.Difficulty
	if row.Difficulty != nil {
		value := question.Difficulty(*row.Difficulty)
		difficulty = &value
	}
	return question.QuestionRecord{
		ID: row.ID, Title: row.Title, Type: question.QuestionType(row.Type),
		BodyMarkdown: row.BodyMarkdown, Difficulty: difficulty,
		SourceName: row.SourceName, SourceURL: row.SourceURL,
		Tags: make([]string, 0), IsArchived: row.IsArchived,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}
