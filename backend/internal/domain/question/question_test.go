package question

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
)

type questionCreatorFunc func(context.Context, QuestionRecord) error

func (f questionCreatorFunc) Create(ctx context.Context, q QuestionRecord) error { return f(ctx, q) }

type questionDetailReaderFunc func(context.Context, string) (QuestionDetail, error)

func (f questionDetailReaderFunc) GetDetail(ctx context.Context, id string) (QuestionDetail, error) {
	return f(ctx, id)
}

func TestServiceCreate(t *testing.T) {
	var saved QuestionRecord
	service := NewQuestionService(questionCreatorFunc(func(_ context.Context, q QuestionRecord) error {
		saved = q
		return nil
	}))
	q, err := service.Create(context.Background(), CreateQuestionInput{Title: " Title ", Type: QuestionTypeKnowledge, BodyMarkdown: "Body", Tags: []string{" go ", "go", "sql"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == "" || q.Title != "Title" || q.CreatedAt.IsZero() || !q.CreatedAt.Equal(q.UpdatedAt) || !reflect.DeepEqual(q.Tags, []string{"go", "sql"}) {
		t.Fatalf("unexpected record: %+v", q)
	}
	if !reflect.DeepEqual(saved, q) {
		t.Fatalf("repository received %+v, want %+v", saved, q)
	}
}

func TestServiceCreateInvalidInput(t *testing.T) {
	badDifficulty := Difficulty("invalid")
	for _, name := range []string{"title", "body", "type", "difficulty", "tag"} {
		t.Run(name, func(t *testing.T) {
			input := CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body"}
			switch name {
			case "title":
				input.Title = " "
			case "body":
				input.BodyMarkdown = " "
			case "type":
				input.Type = "invalid"
			case "difficulty":
				input.Difficulty = &badDifficulty
			case "tag":
				input.Tags = []string{" "}
			}
			service := NewQuestionService(questionCreatorFunc(func(context.Context, QuestionRecord) error { t.Fatal("invalid input reached repository"); return nil }))
			q, err := service.Create(context.Background(), input)
			if !errors.Is(err, ErrInvalidInput) || q.ID != "" {
				t.Fatalf("got %+v, %v", q, err)
			}
		})
	}
}

func TestServiceCreateRepositoryError(t *testing.T) {
	want := errors.New("storage failure")
	service := NewQuestionService(questionCreatorFunc(func(context.Context, QuestionRecord) error { return want }))
	q, err := service.Create(context.Background(), CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body"})
	if !errors.Is(err, want) || q.ID != "" {
		t.Fatalf("got %+v, %v", q, err)
	}
}

func TestValidateCreateQuestionInput(t *testing.T) {
	empty := Difficulty("")
	hard := DifficultyHard
	for _, tc := range []struct {
		name       string
		difficulty *Difficulty
		tags       []string
		valid      bool
	}{
		{"optional", nil, nil, true},
		{"hard", &hard, []string{"go"}, true},
		{"empty difficulty", &empty, nil, false},
		{"blank tag", nil, []string{"\t "}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateQuestionInput(CreateQuestionInput{Title: "Title", Type: QuestionTypeKnowledge, BodyMarkdown: "Body", Difficulty: tc.difficulty, Tags: tc.tags})
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected invalid input, got %v", err)
			}
			var fields validator.ValidationErrors
			if !errors.As(err, &fields) || len(fields) == 0 {
				t.Fatalf("missing field errors: %v", err)
			}
		})
	}
}

func TestValidateEnumValues(t *testing.T) {
	for _, value := range []QuestionType{QuestionTypeAlgorithm, QuestionTypeKnowledge, QuestionTypeSystemDesign, QuestionTypeBehavioral, QuestionTypeOther} {
		if err := ValidateQuestionType(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []Difficulty{DifficultyEasy, DifficultyMedium, DifficultyHard} {
		if err := ValidateDifficulty(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []AnswerResult{AnswerResultSkipped, AnswerResultIncorrect, AnswerResultPartial, AnswerResultCorrect} {
		if err := ValidateAnswerResult(value); err != nil {
			t.Fatal(err)
		}
	}
	for _, err := range []error{ValidateQuestionType("invalid"), ValidateDifficulty(""), ValidateAnswerResult("invalid")} {
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("expected invalid input, got %v", err)
		}
	}
}

func TestCreateQuestionHandler(t *testing.T) {
	var saved QuestionRecord
	service := NewQuestionService(questionCreatorFunc(func(_ context.Context, record QuestionRecord) error {
		saved = record
		return nil
	}))
	handler := CreateQuestionHandler(service)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/questions", strings.NewReader(`{
		"title":" Two Sum ",
		"type":"algorithm",
		"bodyMarkdown":"Find two numbers.",
		"tags":[" array ","array"]
	}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	var got QuestionRecord
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Title != "Two Sum" || !reflect.DeepEqual(got.Tags, []string{"array"}) {
		t.Fatalf("unexpected response: %+v", got)
	}
	if !reflect.DeepEqual(saved, got) {
		t.Fatalf("saved record differs from response: saved=%+v response=%+v", saved, got)
	}
}

func TestCreateQuestionHandlerRejectsInvalidJSONAndInput(t *testing.T) {
	service := NewQuestionService(questionCreatorFunc(func(context.Context, QuestionRecord) error {
		t.Fatal("invalid request reached repository")
		return nil
	}))
	handler := CreateQuestionHandler(service)
	for _, body := range []string{
		`{"title":`,
		`{"title":"","type":"algorithm","bodyMarkdown":"body"}`,
		`{"title":"title","type":"algorithm","bodyMarkdown":"body","unexpected":true}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/questions", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected status 400, got %d", body, response.Code)
		}
	}
}

func TestGetQuestionHandler(t *testing.T) {
	service := NewQuestionServiceWithDependencies(QuestionServiceDependencies{
		DetailReader: questionDetailReaderFunc(func(_ context.Context, id string) (QuestionDetail, error) {
			if id == "missing" {
				return QuestionDetail{}, ErrNotFound
			}
			return QuestionDetail{Question: QuestionRecord{ID: id, Title: "Two Sum", Type: QuestionTypeAlgorithm, BodyMarkdown: "body"}, Answers: []AnswerAttempt{}, Reviews: []MistakeReview{}, Attachments: []Attachment{}}, nil
		}),
	})
	router := chi.NewRouter()
	RegisterQuestionRoutes(router, service)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/questions/q1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var got QuestionDetail
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Question.ID != "q1" || got.Question.Title != "Two Sum" {
		t.Fatalf("unexpected detail: %+v", got)
	}

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/questions/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", missing.Code, missing.Body.String())
	}
}
