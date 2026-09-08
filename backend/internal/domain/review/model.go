package review

import "time"

type Record struct {
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

type CreateInput struct {
	AnswerAttemptID    *string `json:"answerAttemptId"`
	MistakeCategory    string  `json:"mistakeCategory"`
	ReviewMarkdown     string  `json:"reviewMarkdown"`
	CorrectionMarkdown string  `json:"correctionMarkdown"`
	KeyConclusions     string  `json:"keyConclusions"`
	AIContentMarkdown  string  `json:"aiContentMarkdown"`
	AISource           string  `json:"aiSource"`
}

type UpdateInput struct {
	AnswerAttemptID    **string `json:"answerAttemptId"`
	MistakeCategory    *string  `json:"mistakeCategory"`
	ReviewMarkdown     *string  `json:"reviewMarkdown"`
	CorrectionMarkdown *string  `json:"correctionMarkdown"`
	KeyConclusions     *string  `json:"keyConclusions"`
	AIContentMarkdown  *string  `json:"aiContentMarkdown"`
	AISource           *string  `json:"aiSource"`
}
