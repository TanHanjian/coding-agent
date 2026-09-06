package question

import (
	"context"
	"io"
	"time"
)

type QuestionType string

const (
	QuestionTypeAlgorithm    QuestionType = "algorithm"
	QuestionTypeKnowledge    QuestionType = "knowledge"
	QuestionTypeSystemDesign QuestionType = "system_design"
	QuestionTypeBehavioral   QuestionType = "behavioral"
	QuestionTypeOther        QuestionType = "other"
)

type Difficulty string

const (
	DifficultyEasy   Difficulty = "easy"
	DifficultyMedium Difficulty = "medium"
	DifficultyHard   Difficulty = "hard"
)

type AnswerResult string

const (
	AnswerResultSkipped   AnswerResult = "skipped"
	AnswerResultIncorrect AnswerResult = "incorrect"
	AnswerResultPartial   AnswerResult = "partial"
	AnswerResultCorrect   AnswerResult = "correct"
)

type QuestionRecord struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Type         QuestionType `json:"type"`
	BodyMarkdown string       `json:"bodyMarkdown"`
	Difficulty   *Difficulty  `json:"difficulty,omitempty"`
	SourceName   string       `json:"sourceName,omitempty"`
	SourceURL    string       `json:"sourceUrl,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	IsArchived   bool         `json:"isArchived"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}

type AnswerAttempt struct {
	ID           string       `json:"id"`
	QuestionID   string       `json:"questionId"`
	BodyMarkdown string       `json:"bodyMarkdown"`
	Code         string       `json:"code,omitempty"`
	CodeLanguage string       `json:"codeLanguage,omitempty"`
	Result       AnswerResult `json:"result"`
	DurationMs   *int64       `json:"durationMs,omitempty"`
	CreatedAt    time.Time    `json:"createdAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
}

type MistakeReview struct {
	ID                 string    `json:"id"`
	QuestionID         string    `json:"questionId"`
	AnswerAttemptID    *string   `json:"answerAttemptId,omitempty"`
	MistakeCategory    string    `json:"mistakeCategory,omitempty"`
	ReviewMarkdown     string    `json:"reviewMarkdown"`
	CorrectionMarkdown string    `json:"correctionMarkdown,omitempty"`
	KeyConclusions     string    `json:"keyConclusions,omitempty"`
	AIContentMarkdown  string    `json:"aiContentMarkdown,omitempty"`
	AISource           string    `json:"aiSource,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type Attachment struct {
	ID           string    `json:"id"`
	QuestionID   string    `json:"questionId"`
	OwnerType    string    `json:"ownerType"`
	OwnerID      string    `json:"ownerId"`
	OriginalName string    `json:"originalName"`
	StoredName   string    `json:"storedName"`
	MIMEType     string    `json:"mimeType"`
	SizeBytes    int64     `json:"sizeBytes"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"createdAt"`
}

type QuestionDetail struct {
	Question    QuestionRecord  `json:"question"`
	Answers     []AnswerAttempt `json:"answers"`
	Reviews     []MistakeReview `json:"reviews"`
	Attachments []Attachment    `json:"attachments"`
}

type CreateQuestionInput struct {
	Title        string       `validate:"required,notblank"`
	Type         QuestionType `validate:"required,question_type"`
	BodyMarkdown string       `validate:"required,notblank"`
	Difficulty   *Difficulty  `validate:"omitempty,question_difficulty"`
	SourceName   string
	SourceURL    string
	Tags         []string `validate:"dive,required,notblank"`
}
type UpdateQuestionInput struct {
	Title        *string
	Type         *QuestionType
	BodyMarkdown *string
	Difficulty   **Difficulty
	SourceName   *string
	SourceURL    *string
	Tags         *[]string
}
type CreateAnswerInput struct {
	BodyMarkdown string
	Code         string
	CodeLanguage string
	Result       AnswerResult
	DurationMs   *int64
}
type UpdateAnswerInput struct {
	BodyMarkdown *string
	Code         *string
	CodeLanguage *string
	Result       *AnswerResult
	DurationMs   **int64
}
type CreateReviewInput struct {
	AnswerAttemptID    *string
	MistakeCategory    string
	ReviewMarkdown     string
	CorrectionMarkdown string
	KeyConclusions     string
	AIContentMarkdown  string
	AISource           string
}
type UpdateReviewInput struct {
	AnswerAttemptID    **string
	MistakeCategory    *string
	ReviewMarkdown     *string
	CorrectionMarkdown *string
	KeyConclusions     *string
	AIContentMarkdown  *string
	AISource           *string
}
type AttachmentUploadInput struct {
	QuestionID   string
	OwnerType    string
	OwnerID      string
	OriginalName string
	MIMEType     string
	SizeBytes    int64
	Reader       io.Reader
}
type AttachmentUpload struct {
	TemporaryID string
	StoredName  string
}
type AttachmentCompleteInput struct {
	TemporaryID string
	SHA256      string
}
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

type QuestionSearchQuery struct {
	Text          string
	Types         []QuestionType
	Difficulties  []Difficulty
	Results       []AnswerResult
	Tags          []string
	Source        string
	Archived      *bool
	HasMistakes   *bool
	Page          int
	PageSize      int
	SortBy        string
	SortDirection string
}
type QuestionSearchResult struct {
	Items    []QuestionRecord `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
	Total    int              `json:"total"`
	HasNext  bool             `json:"hasNext"`
}

type TxFunc func(context.Context) error
