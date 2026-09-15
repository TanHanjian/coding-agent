package hot100

import (
	"context"
	"testing"

	"interview-memory-agent/backend/internal/domain/question"
)

func TestManifestHasUniqueCanonicalURLs(t *testing.T) {
	if got := len(Manifest); got != 100 {
		t.Fatalf("manifest count = %d, want 100", got)
	}
	seen := map[string]bool{}
	for _, entry := range Manifest {
		input := entry.input()
		if seen[input.SourceURL] {
			t.Fatalf("duplicate URL %q", input.SourceURL)
		}
		seen[input.SourceURL] = true
		if input.Type != question.QuestionTypeAlgorithm || input.Difficulty == nil || input.SourceName != sourceName {
			t.Fatalf("invalid input for %#v", entry)
		}
	}
}

func TestImportIsIdempotentAndDryRunDoesNotWrite(t *testing.T) {
	first := Manifest[0].input()
	catalog := &catalogStub{results: []question.QuestionSearchResult{{Items: []question.QuestionRecord{{SourceURL: first.SourceURL}}}}}
	importer, err := NewImporter(catalog)
	if err != nil {
		t.Fatal(err)
	}
	result, err := importer.Import(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 99 || result.Skipped != 1 || len(catalog.created) != 0 {
		t.Fatalf("dry run result = %#v, created = %d", result, len(catalog.created))
	}
	result, err = importer.Import(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 99 || len(catalog.created) != 99 {
		t.Fatalf("import result = %#v, created = %d", result, len(catalog.created))
	}
}

type catalogStub struct {
	results  []question.QuestionSearchResult
	created  []question.CreateQuestionInput
	searches int
}

func (s *catalogStub) Search(_ context.Context, _ question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	index := s.searches
	if index >= len(s.results) {
		index = len(s.results) - 1
	}
	r := s.results[index]
	s.searches++
	return r, nil
}
func (s *catalogStub) Create(_ context.Context, in question.CreateQuestionInput) (question.QuestionRecord, error) {
	s.created = append(s.created, in)
	return question.QuestionRecord{}, nil
}
