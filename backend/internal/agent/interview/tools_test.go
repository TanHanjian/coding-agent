package interview

import (
	"context"
	"errors"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/domain/question"
)

func TestSearchQuestionMemoryToolUsesBoundedDomainSearch(t *testing.T) {
	difficulty := question.DifficultyMedium
	searcher := &questionSearcherStub{result: question.QuestionSearchResult{
		Items: []question.QuestionRecord{{
			ID:         "q-1",
			Title:      "二叉树遍历",
			Type:       question.QuestionTypeAlgorithm,
			Difficulty: &difficulty,
			Tags:       []string{"tree"},
		}},
		Total:   7,
		HasNext: true,
	}}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}

	result, err := tool.InvokableRun(context.Background(), `{"query":"二叉树","tags":["tree"],"limit":2}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if searcher.query.Text != "二叉树" || searcher.query.Page != 1 || searcher.query.PageSize != 2 {
		t.Fatalf("search query = %#v, want text and first bounded page", searcher.query)
	}
	if got, want := searcher.query.Tags, []string{"tree"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("search tags = %#v, want %#v", got, want)
	}
	if !strings.Contains(result, `"id":"q-1"`) || !strings.Contains(result, `"hasMore":true`) {
		t.Fatalf("tool result = %s, want compact result payload", result)
	}
}

func TestSearchQuestionMemoryToolRejectsUnboundedOrEmptySearch(t *testing.T) {
	searcher := &questionSearcherStub{}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}

	for _, input := range []string{`{}`, `{"query":"Go","limit":6}`} {
		if _, err := tool.InvokableRun(context.Background(), input); err == nil {
			t.Fatalf("InvokableRun(%s) error = nil, want validation error", input)
		}
	}
	if searcher.calls != 0 {
		t.Fatalf("searcher calls = %d, want 0 for invalid input", searcher.calls)
	}
}

func TestSearchQuestionMemoryToolReturnsSearcherError(t *testing.T) {
	searcher := &questionSearcherStub{err: errors.New("storage unavailable")}
	tool, err := NewSearchQuestionMemoryTool(searcher)
	if err != nil {
		t.Fatalf("NewSearchQuestionMemoryTool() error = %v", err)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"query":"Go"}`); !errors.Is(err, searcher.err) {
		t.Fatalf("InvokableRun() error = %v, want searcher error", err)
	}
}

func TestNewToolsBuildsSearchQuestionMemoryTool(t *testing.T) {
	tools, err := NewTools(ToolDependencies{QuestionSearcher: &questionSearcherStub{}})
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("tool.Info() error = %v", err)
	}
	if info.Name != "search_question_memory" {
		t.Fatalf("tool name = %q, want search_question_memory", info.Name)
	}
}

func TestFakeSearchQuestionsTool(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatal(err)
	}
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "search_questions" {
		t.Fatalf("name=%q", info.Name)
	}
	result, err := tool.InvokableRun(context.Background(), `{"query":"二叉树"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("empty result")
	}
}

type questionSearcherStub struct {
	query  question.QuestionSearchQuery
	result question.QuestionSearchResult
	err    error
	calls  int
}

func (s *questionSearcherStub) Search(_ context.Context, query question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	s.calls++
	s.query = query
	return s.result, s.err
}
