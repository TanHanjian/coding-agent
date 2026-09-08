package repository

import (
	"context"

	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/domain/review"
)

type QuestionRepository interface {
	Create(context.Context, question.QuestionRecord) error
	GetByID(context.Context, string) (question.QuestionRecord, error)
	Exists(context.Context, string) error
	Update(context.Context, question.QuestionRecord) error
	SetArchived(context.Context, string, bool) error
	DeletePermanent(context.Context, string) error
	Search(context.Context, question.QuestionSearchQuery) (question.QuestionSearchResult, error)
	GetDetail(context.Context, string) (question.QuestionDetail, error)
}
type AnswerRepository interface {
	Create(context.Context, answer.Attempt) error
	ListByQuestion(context.Context, string) ([]answer.Attempt, error)
	GetByID(context.Context, string) (answer.Attempt, error)
	Update(context.Context, answer.Attempt) error
	Delete(context.Context, string) error
}
type ReviewRepository interface {
	Create(context.Context, review.Record) error
	GetByID(context.Context, string) (review.Record, error)
	ListByQuestion(context.Context, string) ([]review.Record, error)
	Update(context.Context, review.Record) error
	Delete(context.Context, string) error
}
type AttachmentRepository interface {
	Create(context.Context, question.Attachment) error
	ListByOwner(context.Context, string, string) ([]question.Attachment, error)
	GetByID(context.Context, string) (question.Attachment, error)
	Delete(context.Context, string) error
}
