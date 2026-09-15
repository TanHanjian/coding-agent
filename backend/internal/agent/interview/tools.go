package interview

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"interview-memory-agent/backend/internal/domain/question"
)

const (
	maxQuestionSearchResults      = 5
	maxQuestionContextAnswers     = 3
	maxQuestionContextReviews     = 3
	maxQuestionContextAttachments = 10
	maxQuestionBodyRunes          = 4_000
	maxAnswerBodyRunes            = 2_000
	maxAnswerCodeRunes            = 2_000
	maxReviewBodyRunes            = 2_000
	truncatedTextSuffix           = "\n...[truncated]"
)

// QuestionSearcher is the narrow domain seam required by the search tool.
// The Tool adapter never reads SQLite directly.
type QuestionSearcher interface {
	Search(context.Context, question.QuestionSearchQuery) (question.QuestionSearchResult, error)
}

// QuestionContextReader is the narrow domain seam required by the question
// context tool. It returns the domain's read-only aggregate rather than
// exposing a repository or SQLite adapter to the Agent.
type QuestionContextReader interface {
	Get(context.Context, string) (question.QuestionDetail, error)
}

// ToolDependencies is the composition-time dependency set for the interview
// tool collection. Each field remains a narrow port; add a new field only when
// a new tool needs that domain capability.
type ToolDependencies struct {
	QuestionSearcher      QuestionSearcher
	QuestionContextReader QuestionContextReader
}

// NewTools creates the tool collection available to the interview Agent.
// It is the extension point for later question-detail, review, and attachment
// tools without widening any individual tool's dependency interface.
func NewTools(deps ToolDependencies) ([]tool.BaseTool, error) {
	searchTool, err := NewSearchQuestionMemoryTool(deps.QuestionSearcher)
	if err != nil {
		return nil, err
	}
	contextTool, err := NewGetQuestionContextTool(deps.QuestionContextReader)
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{searchTool, contextTool}, nil
}

type SearchQuestionMemoryInput struct {
	Query string   `json:"query" jsonschema_description:"Text to search in saved interview questions, answers, and reviews"`
	Tags  []string `json:"tags,omitempty" jsonschema_description:"Optional tags that every matched question must have"`
	Limit int      `json:"limit,omitempty" jsonschema_description:"Maximum results from 1 to 5; defaults to 5"`
}

type SearchQuestionMemoryResult struct {
	Items   []QuestionMemoryItem `json:"items"`
	Total   int                  `json:"total"`
	HasMore bool                 `json:"hasMore"`
}

// QuestionMemoryItem deliberately exposes only a compact search summary.
// The get_question_context tool is responsible for larger, bounded evidence.
type QuestionMemoryItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Difficulty string   `json:"difficulty,omitempty"`
	Tags       []string `json:"tags"`
}

// NewSearchQuestionMemoryTool creates the first real, read-only Agent tool.
// It searches the user's saved interview-question memory through the domain
// service and bounds its result size before returning data to the model.
func NewSearchQuestionMemoryTool(searcher QuestionSearcher) (tool.InvokableTool, error) {
	if searcher == nil {
		return nil, errors.New("search question memory tool: question searcher is required")
	}

	return toolutils.InferTool[SearchQuestionMemoryInput, SearchQuestionMemoryResult](
		"search_question_memory",
		"Search the user's saved interview questions when existing question, answer, or review material is needed. Returns compact summaries and question IDs only.",
		func(ctx context.Context, in SearchQuestionMemoryInput) (SearchQuestionMemoryResult, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" && len(in.Tags) == 0 {
				return SearchQuestionMemoryResult{}, errors.New("search question memory tool: query or tags are required")
			}

			limit := in.Limit
			if limit == 0 {
				limit = maxQuestionSearchResults
			}
			if limit < 1 || limit > maxQuestionSearchResults {
				return SearchQuestionMemoryResult{}, errors.New("search question memory tool: limit must be between 1 and 5")
			}

			found, err := searcher.Search(ctx, question.QuestionSearchQuery{
				Text:     query,
				Tags:     in.Tags,
				Page:     1,
				PageSize: limit,
			})
			if err != nil {
				return SearchQuestionMemoryResult{}, err
			}

			items := make([]QuestionMemoryItem, 0, len(found.Items))
			for _, item := range found.Items {
				difficulty := ""
				if item.Difficulty != nil {
					difficulty = string(*item.Difficulty)
				}
				items = append(items, QuestionMemoryItem{
					ID:         item.ID,
					Title:      item.Title,
					Type:       string(item.Type),
					Difficulty: difficulty,
					Tags:       append([]string(nil), item.Tags...),
				})
			}
			return SearchQuestionMemoryResult{Items: items, Total: found.Total, HasMore: found.HasNext}, nil
		},
	)
}

type GetQuestionContextInput struct {
	QuestionID string `json:"questionId" jsonschema_description:"Saved interview question ID, usually returned by search_question_memory"`
}

// GetQuestionContextResult is a bounded view of the question aggregate. The
// truncation flags tell the model that it must not infer absent details.
type GetQuestionContextResult struct {
	Found                bool                        `json:"found"`
	Question             QuestionContextQuestion     `json:"question,omitempty"`
	Answers              []QuestionContextAnswer     `json:"answers"`
	AnswersTruncated     bool                        `json:"answersTruncated"`
	Reviews              []QuestionContextReview     `json:"reviews"`
	ReviewsTruncated     bool                        `json:"reviewsTruncated"`
	Attachments          []QuestionContextAttachment `json:"attachments"`
	AttachmentsTruncated bool                        `json:"attachmentsTruncated"`
}

type QuestionContextQuestion struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Type         string   `json:"type"`
	Difficulty   string   `json:"difficulty,omitempty"`
	Tags         []string `json:"tags"`
	BodyMarkdown string   `json:"bodyMarkdown"`
	SourceName   string   `json:"sourceName,omitempty"`
	SourceURL    string   `json:"sourceUrl,omitempty"`
}

type QuestionContextAnswer struct {
	ID           string `json:"id"`
	Result       string `json:"result"`
	BodyMarkdown string `json:"bodyMarkdown"`
	Code         string `json:"code,omitempty"`
	CodeLanguage string `json:"codeLanguage,omitempty"`
	DurationMs   *int64 `json:"durationMs,omitempty"`
}

type QuestionContextReview struct {
	ID                 string `json:"id"`
	AnswerAttemptID    string `json:"answerAttemptId,omitempty"`
	MistakeCategory    string `json:"mistakeCategory,omitempty"`
	ReviewMarkdown     string `json:"reviewMarkdown"`
	CorrectionMarkdown string `json:"correctionMarkdown,omitempty"`
	KeyConclusions     string `json:"keyConclusions,omitempty"`
}

// QuestionContextAttachment intentionally includes metadata only. Reading or
// extracting attachment content has separate MIME, size, and safety concerns.
type QuestionContextAttachment struct {
	ID           string `json:"id"`
	OriginalName string `json:"originalName"`
	MIMEType     string `json:"mimeType"`
	SizeBytes    int64  `json:"sizeBytes"`
}

// NewGetQuestionContextTool creates the read-only follow-up tool for a
// question ID returned by search_question_memory. Its output is bounded before
// it reaches the model, and it never exposes storage implementation errors.
func NewGetQuestionContextTool(reader QuestionContextReader) (tool.InvokableTool, error) {
	if reader == nil {
		return nil, errors.New("get question context tool: question context reader is required")
	}

	return toolutils.InferTool[GetQuestionContextInput, GetQuestionContextResult](
		"get_question_context",
		"Read bounded question, answer, review, and attachment metadata for one saved question ID. Use after search_question_memory when detailed evidence is needed.",
		func(ctx context.Context, in GetQuestionContextInput) (GetQuestionContextResult, error) {
			questionID := strings.TrimSpace(in.QuestionID)
			if questionID == "" {
				return GetQuestionContextResult{}, errors.New("get question context tool: questionId is required")
			}

			detail, err := reader.Get(ctx, questionID)
			if errors.Is(err, question.ErrNotFound) {
				return GetQuestionContextResult{Found: false}, nil
			}
			if err != nil {
				return GetQuestionContextResult{}, errors.New("get question context tool: question lookup failed")
			}

			return toQuestionContextResult(detail), nil
		},
	)
}

func toQuestionContextResult(detail question.QuestionDetail) GetQuestionContextResult {
	difficulty := ""
	if detail.Question.Difficulty != nil {
		difficulty = string(*detail.Question.Difficulty)
	}

	answerCount := min(len(detail.Answers), maxQuestionContextAnswers)
	answers := make([]QuestionContextAnswer, 0, answerCount)
	for _, answer := range detail.Answers[:answerCount] {
		answers = append(answers, QuestionContextAnswer{
			ID:           answer.ID,
			Result:       string(answer.Result),
			BodyMarkdown: truncateToolText(answer.BodyMarkdown, maxAnswerBodyRunes),
			Code:         truncateToolText(answer.Code, maxAnswerCodeRunes),
			CodeLanguage: answer.CodeLanguage,
			DurationMs:   answer.DurationMs,
		})
	}

	reviewCount := min(len(detail.Reviews), maxQuestionContextReviews)
	reviews := make([]QuestionContextReview, 0, reviewCount)
	for _, review := range detail.Reviews[:reviewCount] {
		answerAttemptID := ""
		if review.AnswerAttemptID != nil {
			answerAttemptID = *review.AnswerAttemptID
		}
		reviews = append(reviews, QuestionContextReview{
			ID:                 review.ID,
			AnswerAttemptID:    answerAttemptID,
			MistakeCategory:    review.MistakeCategory,
			ReviewMarkdown:     truncateToolText(review.ReviewMarkdown, maxReviewBodyRunes),
			CorrectionMarkdown: truncateToolText(review.CorrectionMarkdown, maxReviewBodyRunes),
			KeyConclusions:     truncateToolText(review.KeyConclusions, maxReviewBodyRunes),
		})
	}

	attachmentCount := min(len(detail.Attachments), maxQuestionContextAttachments)
	attachments := make([]QuestionContextAttachment, 0, attachmentCount)
	for _, attachment := range detail.Attachments[:attachmentCount] {
		attachments = append(attachments, QuestionContextAttachment{
			ID:           attachment.ID,
			OriginalName: attachment.OriginalName,
			MIMEType:     attachment.MIMEType,
			SizeBytes:    attachment.SizeBytes,
		})
	}

	return GetQuestionContextResult{
		Found: true,
		Question: QuestionContextQuestion{
			ID:           detail.Question.ID,
			Title:        detail.Question.Title,
			Type:         string(detail.Question.Type),
			Difficulty:   difficulty,
			Tags:         append([]string(nil), detail.Question.Tags...),
			BodyMarkdown: truncateToolText(detail.Question.BodyMarkdown, maxQuestionBodyRunes),
			SourceName:   detail.Question.SourceName,
			SourceURL:    detail.Question.SourceURL,
		},
		Answers:              answers,
		AnswersTruncated:     len(detail.Answers) > answerCount,
		Reviews:              reviews,
		ReviewsTruncated:     len(detail.Reviews) > reviewCount,
		Attachments:          attachments,
		AttachmentsTruncated: len(detail.Attachments) > attachmentCount,
	}
}

func truncateToolText(value string, maximumRunes int) string {
	if maximumRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maximumRunes {
		return value
	}
	return string(runes[:maximumRunes]) + truncatedTextSuffix
}

type FakeSearchInput struct {
	Query string `json:"query" jsonschema_description:"Question search text"`
}

type FakeSearchResult struct {
	Items []FakeSearchItem `json:"items"`
}

type FakeSearchItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// NewFakeSearchQuestionsTool is a deterministic fixture for Tool Calling tests.
func NewFakeSearchQuestionsTool() (tool.InvokableTool, error) {
	return toolutils.InferTool[FakeSearchInput, FakeSearchResult](
		"search_questions",
		"Search interview questions by text (development fixture).",
		func(_ context.Context, in FakeSearchInput) (FakeSearchResult, error) {
			if in.Query == "" {
				return FakeSearchResult{Items: []FakeSearchItem{}}, nil
			}
			return FakeSearchResult{Items: []FakeSearchItem{{ID: "fake-q-1", Title: "Fixture: " + in.Query}}}, nil
		},
	)
}
