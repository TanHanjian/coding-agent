package sqlite

import (
	"fmt"
	"time"

	"interview-memory-agent/backend/internal/domain/question"
)

// Persistence rows mirror the existing tables; clocks and associations stay explicit.
type answerRow struct {
	ID           string `gorm:"column:id;primaryKey"`
	QuestionID   string `gorm:"column:question_id"`
	BodyMarkdown string `gorm:"column:body_markdown"`
	Code         string `gorm:"column:code"`
	CodeLanguage string `gorm:"column:code_language"`
	Result       string `gorm:"column:result"`
	DurationMs   *int64 `gorm:"column:duration_ms"`
	CreatedAt    string `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    string `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (answerRow) TableName() string { return "answer_attempts" }

func answerToRow(record question.AnswerAttempt) answerRow {
	return answerRow{
		ID: record.ID, QuestionID: record.QuestionID, BodyMarkdown: record.BodyMarkdown,
		Code: record.Code, CodeLanguage: record.CodeLanguage, Result: string(record.Result),
		DurationMs: record.DurationMs, CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
	}
}

func (row answerRow) record() (question.AnswerAttempt, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return question.AnswerAttempt{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return question.AnswerAttempt{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return question.AnswerAttempt{
		ID: row.ID, QuestionID: row.QuestionID, BodyMarkdown: row.BodyMarkdown,
		Code: row.Code, CodeLanguage: row.CodeLanguage, Result: question.AnswerResult(row.Result),
		DurationMs: row.DurationMs, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

type reviewRow struct {
	ID                 string  `gorm:"column:id;primaryKey"`
	QuestionID         string  `gorm:"column:question_id"`
	AnswerAttemptID    *string `gorm:"column:answer_attempt_id"`
	MistakeCategory    string  `gorm:"column:mistake_category"`
	ReviewMarkdown     string  `gorm:"column:review_markdown"`
	CorrectionMarkdown string  `gorm:"column:correction_markdown"`
	KeyConclusions     string  `gorm:"column:key_conclusions"`
	AIContentMarkdown  string  `gorm:"column:ai_content_markdown"`
	AISource           string  `gorm:"column:ai_source"`
	CreatedAt          string  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt          string  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (reviewRow) TableName() string { return "mistake_reviews" }

func reviewToRow(record question.MistakeReview) reviewRow {
	return reviewRow{
		ID: record.ID, QuestionID: record.QuestionID, AnswerAttemptID: record.AnswerAttemptID,
		MistakeCategory: record.MistakeCategory, ReviewMarkdown: record.ReviewMarkdown,
		CorrectionMarkdown: record.CorrectionMarkdown, KeyConclusions: record.KeyConclusions,
		AIContentMarkdown: record.AIContentMarkdown, AISource: record.AISource,
		CreatedAt: formatTime(record.CreatedAt), UpdatedAt: formatTime(record.UpdatedAt),
	}
}

func (row reviewRow) record() (question.MistakeReview, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("parse review created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt)
	if err != nil {
		return question.MistakeReview{}, fmt.Errorf("parse review updated_at: %w", err)
	}
	return question.MistakeReview{
		ID: row.ID, QuestionID: row.QuestionID, AnswerAttemptID: row.AnswerAttemptID,
		MistakeCategory: row.MistakeCategory, ReviewMarkdown: row.ReviewMarkdown,
		CorrectionMarkdown: row.CorrectionMarkdown, KeyConclusions: row.KeyConclusions,
		AIContentMarkdown: row.AIContentMarkdown, AISource: row.AISource,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

type attachmentRow struct {
	ID           string `gorm:"column:id;primaryKey"`
	QuestionID   string `gorm:"column:question_id"`
	OwnerType    string `gorm:"column:owner_type"`
	OwnerID      string `gorm:"column:owner_id"`
	OriginalName string `gorm:"column:original_name"`
	StoredName   string `gorm:"column:stored_name"`
	MIMEType     string `gorm:"column:mime_type"`
	SizeBytes    int64  `gorm:"column:size_bytes"`
	SHA256       string `gorm:"column:sha256"`
	CreatedAt    string `gorm:"column:created_at;autoCreateTime:false"`
}

func (attachmentRow) TableName() string { return "attachments" }

func attachmentToRow(record question.Attachment) attachmentRow {
	return attachmentRow{
		ID: record.ID, QuestionID: record.QuestionID, OwnerType: record.OwnerType, OwnerID: record.OwnerID,
		OriginalName: record.OriginalName, StoredName: record.StoredName, MIMEType: record.MIMEType,
		SizeBytes: record.SizeBytes, SHA256: record.SHA256, CreatedAt: formatTime(record.CreatedAt),
	}
}

func (row attachmentRow) record() (question.Attachment, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return question.Attachment{}, fmt.Errorf("parse created_at: %w", err)
	}
	return question.Attachment{
		ID: row.ID, QuestionID: row.QuestionID, OwnerType: row.OwnerType, OwnerID: row.OwnerID,
		OriginalName: row.OriginalName, StoredName: row.StoredName, MIMEType: row.MIMEType,
		SizeBytes: row.SizeBytes, SHA256: row.SHA256, CreatedAt: createdAt,
	}, nil
}
