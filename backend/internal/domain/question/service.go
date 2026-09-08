package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type Service interface {
	Create(context.Context, CreateQuestionInput) (QuestionRecord, error)
	Get(context.Context, string) (QuestionDetail, error)
	Update(context.Context, string, UpdateQuestionInput) (QuestionRecord, error)
	Archive(context.Context, string) error
	Restore(context.Context, string) error
	DeletePermanent(context.Context, string) error
	Search(context.Context, QuestionSearchQuery) (QuestionSearchResult, error)
}

// QuestionService is retained as the previous public name.
type QuestionService = Service

type AttachmentService interface {
	PrepareUpload(context.Context, AttachmentUploadInput) (AttachmentUpload, error)
	CompleteUpload(context.Context, AttachmentCompleteInput) (Attachment, error)
	Open(context.Context, string) (ReadSeekCloser, Attachment, error)
	Delete(context.Context, string) error
}

// Ports live in the domain package, so the service does not import the
// infrastructure-level repository package.
type Creator interface {
	Create(context.Context, QuestionRecord) error
}
type DetailReader interface {
	GetDetail(context.Context, string) (QuestionDetail, error)
}
type Updater interface {
	GetByID(context.Context, string) (QuestionRecord, error)
	Update(context.Context, QuestionRecord) error
}
type ArchiveWriter interface {
	SetArchived(context.Context, string, bool) error
}
type Deleter interface {
	DeletePermanent(context.Context, string) error
}
type Searcher interface {
	Search(context.Context, QuestionSearchQuery) (QuestionSearchResult, error)
}

type Dependencies struct {
	Creator       Creator
	DetailReader  DetailReader
	Updater       Updater
	ArchiveWriter ArchiveWriter
	Deleter       Deleter
	Searcher      Searcher
}

type service struct {
	creator       Creator
	detailReader  DetailReader
	updater       Updater
	archiveWriter ArchiveWriter
	deleter       Deleter
	searcher      Searcher
}

func NewService(deps Dependencies) Service {
	return service{creator: deps.Creator, detailReader: deps.DetailReader, updater: deps.Updater, archiveWriter: deps.ArchiveWriter, deleter: deps.Deleter, searcher: deps.Searcher}
}

// Deprecated: use NewService.
type UnimplementedQuestionService = service

// Deprecated: use Dependencies.
type QuestionServiceDependencies = Dependencies

// Deprecated: use Creator.
type QuestionCreator = Creator

// NewQuestionService keeps the existing create-only construction path.
func NewQuestionService(creator Creator) Service                   { return NewService(Dependencies{Creator: creator}) }
func NewQuestionServiceWithDependencies(deps Dependencies) Service { return NewService(deps) }

func (s service) Create(ctx context.Context, input CreateQuestionInput) (QuestionRecord, error) {
	if s.creator == nil {
		return QuestionRecord{}, errors.New("question service: creator is not configured")
	}
	if err := ValidateCreateQuestionInput(input); err != nil {
		return QuestionRecord{}, err
	}
	tags, err := normalizeTags(input.Tags)
	if err != nil {
		return QuestionRecord{}, err
	}
	id, err := newID()
	if err != nil {
		return QuestionRecord{}, err
	}
	now := time.Now().UTC()
	record := QuestionRecord{ID: id, Title: strings.TrimSpace(input.Title), Type: input.Type, BodyMarkdown: input.BodyMarkdown, Difficulty: input.Difficulty, SourceName: strings.TrimSpace(input.SourceName), SourceURL: strings.TrimSpace(input.SourceURL), Tags: tags, CreatedAt: now, UpdatedAt: now}
	if err := s.creator.Create(ctx, record); err != nil {
		return QuestionRecord{}, err
	}
	return record, nil
}

func (s service) Get(ctx context.Context, id string) (QuestionDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return QuestionDetail{}, ErrInvalidInput
	}
	if s.detailReader == nil {
		return QuestionDetail{}, errors.New("question service: detail reader is not configured")
	}
	return s.detailReader.GetDetail(ctx, id)
}

func (s service) Update(ctx context.Context, id string, input UpdateQuestionInput) (QuestionRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return QuestionRecord{}, ErrInvalidInput
	}
	if s.updater == nil {
		return QuestionRecord{}, errors.New("question service: updater is not configured")
	}
	current, err := s.updater.GetByID(ctx, id)
	if err != nil {
		return QuestionRecord{}, err
	}
	updated := current
	if input.Title != nil {
		updated.Title = strings.TrimSpace(*input.Title)
		if updated.Title == "" {
			return QuestionRecord{}, ErrInvalidInput
		}
	}
	if input.Type != nil {
		if err := ValidateQuestionType(*input.Type); err != nil {
			return QuestionRecord{}, err
		}
		updated.Type = *input.Type
	}
	if input.BodyMarkdown != nil {
		updated.BodyMarkdown = *input.BodyMarkdown
		if strings.TrimSpace(updated.BodyMarkdown) == "" {
			return QuestionRecord{}, ErrInvalidInput
		}
	}
	if input.Difficulty != nil {
		updated.Difficulty = *input.Difficulty
		if updated.Difficulty != nil {
			if err := ValidateDifficulty(*updated.Difficulty); err != nil {
				return QuestionRecord{}, err
			}
		}
	}
	if input.SourceName != nil {
		updated.SourceName = strings.TrimSpace(*input.SourceName)
	}
	if input.SourceURL != nil {
		updated.SourceURL = strings.TrimSpace(*input.SourceURL)
	}
	if input.Tags != nil {
		tags, err := normalizeTags(*input.Tags)
		if err != nil {
			return QuestionRecord{}, err
		}
		updated.Tags = tags
	}
	updated.UpdatedAt = time.Now().UTC()
	toPersist := updated
	if input.Tags == nil {
		toPersist.Tags = nil
	}
	if err := s.updater.Update(ctx, toPersist); err != nil {
		return QuestionRecord{}, err
	}
	return updated, nil
}

func (s service) Archive(ctx context.Context, id string) error { return s.setArchived(ctx, id, true) }
func (s service) Restore(ctx context.Context, id string) error { return s.setArchived(ctx, id, false) }
func (s service) setArchived(ctx context.Context, id string, archived bool) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidInput
	}
	if s.archiveWriter == nil {
		return errors.New("question service: archive writer is not configured")
	}
	return s.archiveWriter.SetArchived(ctx, id, archived)
}
func (s service) DeletePermanent(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidInput
	}
	if s.deleter == nil {
		return errors.New("question service: deleter is not configured")
	}
	return s.deleter.DeletePermanent(ctx, id)
}
func (s service) Search(ctx context.Context, query QuestionSearchQuery) (QuestionSearchResult, error) {
	if s.searcher == nil {
		return QuestionSearchResult{}, errors.New("question service: searcher is not configured")
	}
	for _, value := range query.Types {
		if err := ValidateQuestionType(value); err != nil {
			return QuestionSearchResult{}, err
		}
	}
	for _, value := range query.Difficulties {
		if err := ValidateDifficulty(value); err != nil {
			return QuestionSearchResult{}, err
		}
	}
	for _, value := range query.Results {
		if err := ValidateAnswerResult(value); err != nil {
			return QuestionSearchResult{}, err
		}
	}
	query, err := NormalizeSearchQuery(query)
	if err != nil {
		return QuestionSearchResult{}, err
	}
	return s.searcher.Search(ctx, query)
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "q_" + hex.EncodeToString(bytes[:]), nil
}
func normalizeTags(tags []string) ([]string, error) {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, ErrInvalidInput
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized, nil
}

type UnimplementedAttachmentService struct{}

func (UnimplementedAttachmentService) PrepareUpload(context.Context, AttachmentUploadInput) (AttachmentUpload, error) {
	return AttachmentUpload{}, ErrNotImplemented
}
func (UnimplementedAttachmentService) CompleteUpload(context.Context, AttachmentCompleteInput) (Attachment, error) {
	return Attachment{}, ErrNotImplemented
}
func (UnimplementedAttachmentService) Open(context.Context, string) (ReadSeekCloser, Attachment, error) {
	return nil, Attachment{}, ErrNotImplemented
}
func (UnimplementedAttachmentService) Delete(context.Context, string) error { return ErrNotImplemented }
