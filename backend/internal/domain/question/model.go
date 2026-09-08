package question

import (
	"context"
	"io"
	"time"

	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/review"
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

// Deprecated: use answer.Result.
type AnswerResult = answer.Result

const (
	AnswerResultSkipped   = answer.ResultSkipped
	AnswerResultIncorrect = answer.ResultIncorrect
	AnswerResultPartial   = answer.ResultPartial
	AnswerResultCorrect   = answer.ResultCorrect
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

// Deprecated: use answer.Attempt.
type AnswerAttempt = answer.Attempt

// Deprecated: use review.Record.
type MistakeReview = review.Record

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
	Title        string       `json:"title" validate:"required,notblank"`
	Type         QuestionType `json:"type" validate:"required,question_type"`
	BodyMarkdown string       `json:"bodyMarkdown" validate:"required,notblank"`
	Difficulty   *Difficulty  `json:"difficulty,omitempty" validate:"omitempty,question_difficulty"`
	SourceName   string       `json:"sourceName"`
	SourceURL    string       `json:"sourceUrl"`
	Tags         []string     `json:"tags" validate:"dive,required,notblank"`
}
type UpdateQuestionInput struct {
	Title        *string       `json:"title"`
	Type         *QuestionType `json:"type"`
	BodyMarkdown *string       `json:"bodyMarkdown"`
	Difficulty   **Difficulty  `json:"difficulty"`
	SourceName   *string       `json:"sourceName"`
	SourceURL    *string       `json:"sourceUrl"`
	Tags         *[]string     `json:"tags"`
}

// Deprecated: use answer.CreateInput.
type CreateAnswerInput = answer.CreateInput

// Deprecated: use answer.UpdateInput.
type UpdateAnswerInput = answer.UpdateInput

// Deprecated: use review.CreateInput.
type CreateReviewInput = review.CreateInput

// Deprecated: use review.UpdateInput.
type UpdateReviewInput = review.UpdateInput
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
