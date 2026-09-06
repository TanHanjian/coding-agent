package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type QuestionService interface {
	Create(context.Context, CreateQuestionInput) (QuestionRecord, error)
	Get(context.Context, string) (QuestionDetail, error)
	Update(context.Context, string, UpdateQuestionInput) (QuestionRecord, error)
	Archive(context.Context, string) error
	Restore(context.Context, string) error
	DeletePermanent(context.Context, string) error
	Search(context.Context, QuestionSearchQuery) (QuestionSearchResult, error)
}
type AnswerService interface {
	Create(context.Context, string, CreateAnswerInput) (AnswerAttempt, error)
	Update(context.Context, string, UpdateAnswerInput) (AnswerAttempt, error)
	List(context.Context, string) ([]AnswerAttempt, error)
	Delete(context.Context, string) error
}
type ReviewService interface {
	Create(context.Context, string, CreateReviewInput) (MistakeReview, error)
	Update(context.Context, string, UpdateReviewInput) (MistakeReview, error)
	List(context.Context, string) ([]MistakeReview, error)
	Delete(context.Context, string) error
}
type AttachmentService interface {
	PrepareUpload(context.Context, AttachmentUploadInput) (AttachmentUpload, error)
	CompleteUpload(context.Context, AttachmentCompleteInput) (Attachment, error)
	Open(context.Context, string) (ReadSeekCloser, Attachment, error)
	Delete(context.Context, string) error
}

// QuestionCreator is the persistence capability needed by Create.
type QuestionCreator interface {
	Create(context.Context, QuestionRecord) error
}

type UnimplementedQuestionService struct {
	repository QuestionCreator
}

func NewQuestionService(repository QuestionCreator) UnimplementedQuestionService {
	return UnimplementedQuestionService{repository: repository}
}

// Create 创建一条题目记录。
// 预期步骤：校验标题、正文、题型、难度和标签；生成题目 ID；在事务中写入题目及标签；最后返回完整题目。
func (s UnimplementedQuestionService) Create(ctx context.Context, input CreateQuestionInput) (QuestionRecord, error) {
	if s.repository == nil {
		return QuestionRecord{}, errors.New("question service: repository is not configured")
	}
	if err := ValidateCreateQuestionInput(input); err != nil {
		return QuestionRecord{}, err
	}

	tags := make([]string, 0, len(input.Tags))
	seen := make(map[string]struct{}, len(input.Tags))
	for _, tag := range input.Tags {
		tag = strings.TrimSpace(tag)
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}

	id, err := newQuestionID()
	if err != nil {
		return QuestionRecord{}, err
	}
	now := time.Now().UTC()
	record := QuestionRecord{
		ID:           id,
		Title:        strings.TrimSpace(input.Title),
		Type:         input.Type,
		BodyMarkdown: input.BodyMarkdown,
		Difficulty:   input.Difficulty,
		SourceName:   strings.TrimSpace(input.SourceName),
		SourceURL:    strings.TrimSpace(input.SourceURL),
		Tags:         tags,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repository.Create(ctx, record); err != nil {
		return QuestionRecord{}, err
	}
	return record, nil
}

func newQuestionID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "q_" + hex.EncodeToString(bytes[:]), nil
}

// The remaining service methods are still pending implementation.

// Get 查询题目详情，并同时组装作答、复盘和附件元数据。
// 预期步骤：校验 questionID；读取题目；读取关联数据；即使附件缺失也返回可用的题目详情。
func (UnimplementedQuestionService) Get(context.Context, string) (QuestionDetail, error) {
	return QuestionDetail{}, ErrNotImplemented
}

// Update 更新题目的非空字段；Tags 等集合字段应按输入整体替换，并在事务中保持题目与标签一致。
func (UnimplementedQuestionService) Update(context.Context, string, UpdateQuestionInput) (QuestionRecord, error) {
	return QuestionRecord{}, ErrNotImplemented
}

// Archive 将题目标记为已归档，使其默认不出现在未归档搜索结果中。
func (UnimplementedQuestionService) Archive(context.Context, string) error { return ErrNotImplemented }

// Restore 取消题目的归档状态，使其重新参与默认搜索。
func (UnimplementedQuestionService) Restore(context.Context, string) error { return ErrNotImplemented }

// DeletePermanent 永久删除题目及其作答、复盘、标签和附件元数据关联记录；整个过程必须在一个事务内完成。
func (UnimplementedQuestionService) DeletePermanent(context.Context, string) error {
	return ErrNotImplemented
}

// Search 按文本、题型、难度、结果、标签和来源组合筛选题目，并执行 offset 分页和稳定排序。
// 预期步骤：先调用 NormalizeSearchQuery；标签条件要求全部匹配；返回总数及 HasNext。
func (UnimplementedQuestionService) Search(context.Context, QuestionSearchQuery) (QuestionSearchResult, error) {
	return QuestionSearchResult{}, ErrNotImplemented
}

type UnimplementedAnswerService struct{}

// Create 为指定题目新增一次作答记录，并校验结果、耗时和题目存在性。
func (UnimplementedAnswerService) Create(context.Context, string, CreateAnswerInput) (AnswerAttempt, error) {
	return AnswerAttempt{}, ErrNotImplemented
}

// Update 按指针字段更新作答记录；只有实际提供的字段才改变，结果和耗时仍需重新校验。
func (UnimplementedAnswerService) Update(context.Context, string, UpdateAnswerInput) (AnswerAttempt, error) {
	return AnswerAttempt{}, ErrNotImplemented
}

// List 返回指定题目的全部作答记录，通常按创建时间排序。
func (UnimplementedAnswerService) List(context.Context, string) ([]AnswerAttempt, error) {
	return nil, ErrNotImplemented
}

// Delete 删除一条作答记录，并处理其被复盘引用时的关联约束。
func (UnimplementedAnswerService) Delete(context.Context, string) error { return ErrNotImplemented }

type UnimplementedReviewService struct{}

// Create 为指定题目新增复盘记录，可选地绑定一次具体作答。
func (UnimplementedReviewService) Create(context.Context, string, CreateReviewInput) (MistakeReview, error) {
	return MistakeReview{}, ErrNotImplemented
}

// Update 按指针字段部分更新复盘内容，并确认绑定的作答仍属于同一题目。
func (UnimplementedReviewService) Update(context.Context, string, UpdateReviewInput) (MistakeReview, error) {
	return MistakeReview{}, ErrNotImplemented
}

// List 返回指定题目的复盘记录，供详情页展示和错题筛选使用。
func (UnimplementedReviewService) List(context.Context, string) ([]MistakeReview, error) {
	return nil, ErrNotImplemented
}

// Delete 删除指定复盘记录，并维护相关外键关系。
func (UnimplementedReviewService) Delete(context.Context, string) error { return ErrNotImplemented }

type UnimplementedAttachmentService struct{}

// PrepareUpload 校验上传元数据并把文件内容写入临时文件，返回临时 ID 和最终存储名。
// 此阶段不提交附件数据库记录，便于在校验完成前安全取消上传。
func (UnimplementedAttachmentService) PrepareUpload(context.Context, AttachmentUploadInput) (AttachmentUpload, error) {
	return AttachmentUpload{}, ErrNotImplemented
}

// CompleteUpload 校验临时文件的 SHA-256，原子移动到正式位置，并在事务中保存附件元数据。
func (UnimplementedAttachmentService) CompleteUpload(context.Context, AttachmentCompleteInput) (Attachment, error) {
	return Attachment{}, ErrNotImplemented
}

// Open 根据附件 ID 打开正式文件，同时返回附件元数据；调用方负责关闭返回的文件句柄。
func (UnimplementedAttachmentService) Open(context.Context, string) (ReadSeekCloser, Attachment, error) {
	return nil, Attachment{}, ErrNotImplemented
}

// Delete 删除附件文件及其元数据；删除前应确认没有仍需保留的引用。
func (UnimplementedAttachmentService) Delete(context.Context, string) error { return ErrNotImplemented }
