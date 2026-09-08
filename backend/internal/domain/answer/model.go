package answer

import "time"

type Result string

const (
	ResultSkipped   Result = "skipped"
	ResultIncorrect Result = "incorrect"
	ResultPartial   Result = "partial"
	ResultCorrect   Result = "correct"
)

type Attempt struct {
	ID           string    `json:"id"`
	QuestionID   string    `json:"questionId"`
	BodyMarkdown string    `json:"bodyMarkdown"`
	Code         string    `json:"code,omitempty"`
	CodeLanguage string    `json:"codeLanguage,omitempty"`
	Result       Result    `json:"result"`
	DurationMs   *int64    `json:"durationMs,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type CreateInput struct {
	BodyMarkdown string `json:"bodyMarkdown"`
	Code         string `json:"code"`
	CodeLanguage string `json:"codeLanguage"`
	Result       Result `json:"result"`
	DurationMs   *int64 `json:"durationMs"`
}

type UpdateInput struct {
	BodyMarkdown *string `json:"bodyMarkdown"`
	Code         *string `json:"code"`
	CodeLanguage *string `json:"codeLanguage"`
	Result       *Result `json:"result"`
	DurationMs   **int64 `json:"durationMs"`
}
