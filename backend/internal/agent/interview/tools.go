package interview

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"interview-memory-agent/backend/internal/domain/question"
)

const maxQuestionSearchResults = 5

// QuestionSearcher is the narrow domain seam required by the search tool.
// The Tool adapter never reads SQLite directly.
type QuestionSearcher interface {
	Search(context.Context, question.QuestionSearchQuery) (question.QuestionSearchResult, error)
}

// ToolDependencies is the composition-time dependency set for the interview
// tool collection. Each field remains a narrow port; add a new field only when
// a new tool needs that domain capability.
type ToolDependencies struct {
	QuestionSearcher QuestionSearcher
}

// NewTools creates the tool collection available to the interview Agent.
// It is the extension point for later question-detail, review, and attachment
// tools without widening any individual tool's dependency interface.
func NewTools(deps ToolDependencies) ([]tool.BaseTool, error) {
	searchTool, err := NewSearchQuestionMemoryTool(deps.QuestionSearcher)
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{searchTool}, nil
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
// The later get_question_detail tool will be responsible for larger evidence.
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
