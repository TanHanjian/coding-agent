package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/repository"
	"interview-memory-agent/backend/internal/infrastructure/storage"
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
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		row := questionToRow(record)
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("insert question: %w", err)
		}
		return insertQuestionTags(tx, record.ID, record.Tags)
	})
}

func insertQuestionTags(tx *gorm.DB, id string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	rows := make([]questionTagRow, len(tags))
	for i, tag := range tags {
		rows[i] = questionTagRow{QuestionID: id, Tag: tag}
	}
	if err := tx.CreateInBatches(&rows, 100).Error; err != nil {
		return fmt.Errorf("insert question tag: %w", err)
	}
	return nil
}

// Exists checks whether the question is present without exposing its domain model.
func (r *QuestionRepository) Exists(ctx context.Context, id string) error {
	var row questionRow
	result := r.db.ORM(ctx).Select("id").Where("id = ?", id).Take(&row)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return question.ErrNotFound
	}
	if result.Error != nil {
		return fmt.Errorf("check question exists: %w", result.Error)
	}
	return nil
}

func (r *QuestionRepository) GetByID(ctx context.Context, id string) (question.QuestionRecord, error) {
	var row questionRow
	if err := r.db.ORM(ctx).Where("id = ?", id).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return question.QuestionRecord{}, question.ErrNotFound
		}
		return question.QuestionRecord{}, fmt.Errorf("get question by ID: %w", err)
	}
	record, err := row.record()
	if err != nil {
		return question.QuestionRecord{}, err
	}
	items := []question.QuestionRecord{record}
	if err := loadQuestionTags(r.db.ORM(ctx), items); err != nil {
		return question.QuestionRecord{}, err
	}
	return items[0], nil
}

// loadQuestionTags runs after the question query has consumed its rows, so it
// also works with the storage layer's single connection.
func loadQuestionTags(db *gorm.DB, items []question.QuestionRecord) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	indices := make(map[string]int, len(items))
	for i := range items {
		ids[i], indices[items[i].ID] = items[i].ID, i
		items[i].Tags = make([]string, 0)
	}
	var tags []questionTagRow
	if err := db.Where("question_id IN ?", ids).Order("tag").Find(&tags).Error; err != nil {
		return fmt.Errorf("query question tags: %w", err)
	}
	for _, tag := range tags {
		i := indices[tag.QuestionID]
		items[i].Tags = append(items[i].Tags, tag.Tag)
	}
	return nil
}

func (r *QuestionRepository) Update(ctx context.Context, record question.QuestionRecord) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		row := questionToRow(record)
		result := tx.Model(&questionRow{}).Where("id = ?", record.ID).Updates(map[string]any{
			"title": row.Title, "type": row.Type, "body_markdown": row.BodyMarkdown,
			"difficulty": row.Difficulty, "source_name": row.SourceName, "source_url": row.SourceURL,
			"is_archived": row.IsArchived, "updated_at": row.UpdatedAt,
		})
		if err := requireORMAffected(result, "update question"); err != nil {
			return err
		}
		// Nil preserves tags; every non-nil slice replaces the entire set.
		if record.Tags == nil {
			return nil
		}
		if err := tx.Where("question_id = ?", record.ID).Delete(&questionTagRow{}).Error; err != nil {
			return fmt.Errorf("delete question tags: %w", err)
		}
		return insertQuestionTags(tx, record.ID, record.Tags)
	})
}

func (r *QuestionRepository) SetArchived(ctx context.Context, id string, isArchived bool) error {
	result := r.db.ORM(ctx).Model(&questionRow{}).Where("id = ?", id).Updates(map[string]any{
		"is_archived": isArchived, "updated_at": formatTime(time.Now()),
	})
	return requireORMAffected(result, "set question archived")
}

func (r *QuestionRepository) DeletePermanent(ctx context.Context, id string) error {
	return r.db.WithinTx(ctx, func(ctx context.Context, tx *gorm.DB) error {
		// Foreign keys cascade to tags, answers, reviews and attachment metadata.
		return requireORMAffected(tx.Where("id = ?", id).Delete(&questionRow{}), "delete question")
	})
}

func (r *QuestionRepository) Search(ctx context.Context, query question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	query, err := question.NormalizeSearchQuery(query)
	if err != nil {
		return question.QuestionSearchResult{}, err
	}
	filters := questionFilterScopes(query)
	// Count and page each start from a fresh root session. Ordering and page
	// bounds are only applied to the latter query.
	newQuery := func() *gorm.DB {
		return r.db.ORM(ctx).Session(&gorm.Session{NewDB: true}).Model(&questionRow{}).Scopes(filters...)
	}
	var total int64
	if err := newQuery().Count(&total).Error; err != nil {
		return question.QuestionSearchResult{}, fmt.Errorf("count questions: %w", err)
	}
	offset := (query.Page - 1) * query.PageSize
	var rows []questionRow
	if err := newQuery().Scopes(questionOrderScope(query.SortBy, query.SortDirection)).
		Limit(query.PageSize).Offset(offset).Find(&rows).Error; err != nil {
		return question.QuestionSearchResult{}, fmt.Errorf("search questions: %w", err)
	}
	items := make([]question.QuestionRecord, 0, len(rows))
	for _, row := range rows {
		item, err := row.record()
		if err != nil {
			return question.QuestionSearchResult{}, err
		}
		items = append(items, item)
	}
	if err := loadQuestionTags(r.db.ORM(ctx), items); err != nil {
		return question.QuestionSearchResult{}, err
	}
	return question.QuestionSearchResult{
		Items: items, Page: query.Page, PageSize: query.PageSize,
		Total: int(total), HasNext: offset+len(items) < int(total),
	}, nil
}

func (r *QuestionRepository) GetDetail(ctx context.Context, id string) (question.QuestionDetail, error) {
	record, err := r.GetByID(ctx, id)
	if err != nil {
		return question.QuestionDetail{}, err
	}
	attempts, err := NewAnswerRepository(r.db).ListByQuestion(ctx, id)
	if err != nil {
		return question.QuestionDetail{}, fmt.Errorf("list answer attempts: %w", err)
	}
	reviews, err := NewReviewRepository(r.db).ListByQuestion(ctx, id)
	if err != nil {
		return question.QuestionDetail{}, fmt.Errorf("list mistake reviews: %w", err)
	}
	attachments, err := NewAttachmentRepository(r.db).ListByOwner(ctx, "question", id)
	if err != nil {
		return question.QuestionDetail{}, fmt.Errorf("list attachments: %w", err)
	}
	return question.QuestionDetail{
		Question:    record,
		Answers:     attempts,
		Reviews:     reviews,
		Attachments: attachments,
	}, nil
}

var _ repository.QuestionRepository = (*QuestionRepository)(nil)
