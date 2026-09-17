package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"interview-memory-agent/backend/internal/agent/interview"
	"interview-memory-agent/backend/internal/domain/question"
)

type fixtureStore struct {
	mu       sync.Mutex
	fixtures Fixtures
	byID     map[string]question.QuestionDetail
	trace    []ToolTrace
}

func newFixtureStore(fixtures Fixtures) *fixtureStore {
	byID := make(map[string]question.QuestionDetail, len(fixtures.Questions))
	for _, fixture := range fixtures.Questions {
		byID[fixture.Question.ID] = question.QuestionDetail{
			Question:    fixture.Question,
			Answers:     fixture.Answers,
			Reviews:     fixture.Reviews,
			Attachments: fixture.Attachments,
		}
	}
	return &fixtureStore{fixtures: fixtures, byID: byID}
}

func (s *fixtureStore) Search(ctx context.Context, query question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	if err := ctx.Err(); err != nil {
		return question.QuestionSearchResult{}, err
	}
	s.record(ToolTrace{Name: "search_question_memory", Arguments: map[string]any{"query": query.Text, "tags": append([]string(nil), query.Tags...), "limit": query.PageSize}})
	if s.fixtures.SearchError != "" {
		s.updateLastError(s.fixtures.SearchError)
		return question.QuestionSearchResult{}, errors.New(s.fixtures.SearchError)
	}
	items := make([]question.QuestionRecord, 0)
	needle := strings.ToLower(strings.TrimSpace(query.Text))
	for _, fixture := range s.fixtures.Questions {
		q := fixture.Question
		if needle != "" && !strings.Contains(strings.ToLower(q.Title+" "+q.BodyMarkdown+" "+strings.Join(q.Tags, " ")), needle) {
			continue
		}
		if !hasAllTags(q.Tags, query.Tags) {
			continue
		}
		items = append(items, q)
	}
	if query.PageSize > 0 && len(items) > query.PageSize {
		items = items[:query.PageSize]
	}
	return question.QuestionSearchResult{Items: items, Page: 1, PageSize: query.PageSize, Total: len(items), HasNext: false}, nil
}

func (s *fixtureStore) Get(ctx context.Context, id string) (question.QuestionDetail, error) {
	if err := ctx.Err(); err != nil {
		return question.QuestionDetail{}, err
	}
	s.record(ToolTrace{Name: "get_question_context", Arguments: map[string]any{"questionId": id}})
	if message := s.fixtures.ContextError[id]; message != "" {
		s.updateLastError(message)
		return question.QuestionDetail{}, errors.New(message)
	}
	detail, ok := s.byID[id]
	if !ok {
		return question.QuestionDetail{}, question.ErrNotFound
	}
	return detail, nil
}

func (s *fixtureStore) record(trace ToolTrace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trace = append(s.trace, trace)
}
func (s *fixtureStore) updateLastError(_ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.trace) > 0 {
		s.trace[len(s.trace)-1].Error = "tool_error"
	}
}
func (s *fixtureStore) traces() []ToolTrace {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ToolTrace, len(s.trace))
	copy(out, s.trace)
	return out
}

func hasAllTags(have, want []string) bool {
	for _, tag := range want {
		found := false
		for _, item := range have {
			if item == tag {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (s *fixtureStore) Dependencies() interview.ToolDependencies {
	return interview.ToolDependencies{QuestionSearcher: s, QuestionContextReader: s}
}

func (s *fixtureStore) Validate() error {
	for _, fixture := range s.fixtures.Questions {
		if strings.TrimSpace(fixture.Question.ID) == "" {
			return fmt.Errorf("fixture question id is required")
		}
	}
	return nil
}
