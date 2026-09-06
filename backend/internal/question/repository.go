package question

import "context"

type QuestionRepository interface {
	Create(context.Context, QuestionRecord) error
	GetByID(context.Context, string) (QuestionRecord, error)
	Update(context.Context, QuestionRecord) error
	SetArchived(context.Context, string, bool) error
	DeletePermanent(context.Context, string) error
	Search(context.Context, QuestionSearchQuery) (QuestionSearchResult, error)
	GetDetail(context.Context, string) (QuestionDetail, error)
}

type AnswerRepository interface {
	Create(context.Context, AnswerAttempt) error
	ListByQuestion(context.Context, string) ([]AnswerAttempt, error)
	GetByID(context.Context, string) (AnswerAttempt, error)
	Update(context.Context, AnswerAttempt) error
	Delete(context.Context, string) error
}

type ReviewRepository interface {
	Create(context.Context, MistakeReview) error
	GetByID(context.Context, string) (MistakeReview, error)
	ListByQuestion(context.Context, string) ([]MistakeReview, error)
	Update(context.Context, MistakeReview) error
	Delete(context.Context, string) error
}

type AttachmentRepository interface {
	Create(context.Context, Attachment) error
	ListByOwner(context.Context, string, string) ([]Attachment, error)
	GetByID(context.Context, string) (Attachment, error)
	Delete(context.Context, string) error
}
