package eval

import (
	"context"
	"testing"

	"interview-memory-agent/backend/internal/domain/question"
)

func TestFixtureStoreRecordsSearchAndContext(t *testing.T) {
	s := newFixtureStore(Fixtures{Questions: []QuestionFixture{{Question: question.QuestionRecord{ID: "q-1", Title: "单调栈", BodyMarkdown: "栈", Tags: []string{"stack"}}}}})
	if _, err := s.Search(context.Background(), question.QuestionSearchQuery{Text: "单调", PageSize: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(context.Background(), "q-1"); err != nil {
		t.Fatal(err)
	}
	traces := s.traces()
	if len(traces) != 2 || traces[0].Name != "search_question_memory" || traces[1].Name != "get_question_context" {
		t.Fatalf("traces = %#v", traces)
	}
}

func TestFixtureStoreReturnsNotFoundWithoutLeakingDetails(t *testing.T) {
	s := newFixtureStore(Fixtures{})
	_, err := s.Get(context.Background(), "missing")
	if err != question.ErrNotFound {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}
