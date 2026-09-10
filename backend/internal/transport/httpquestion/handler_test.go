package httpquestion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	question "interview-memory-agent/backend/internal/domain/question"
)

type creatorFunc func(context.Context, question.QuestionRecord) error

func (f creatorFunc) Create(ctx context.Context, record question.QuestionRecord) error {
	return f(ctx, record)
}

type detailReaderFunc func(context.Context, string) (question.QuestionDetail, error)

func (f detailReaderFunc) GetDetail(ctx context.Context, id string) (question.QuestionDetail, error) {
	return f(ctx, id)
}

func TestCreateHandler(t *testing.T) {
	var saved question.QuestionRecord
	service := question.NewQuestionService(creatorFunc(func(_ context.Context, record question.QuestionRecord) error {
		saved = record
		return nil
	}))
	response := httptest.NewRecorder()
	CreateHandler(service).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/questions", strings.NewReader(`{"title":" Two Sum ","type":"algorithm","bodyMarkdown":"Find two numbers.","tags":[" array ","array"]}`)))
	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	var got question.QuestionRecord
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Title != "Two Sum" || !reflect.DeepEqual(got.Tags, []string{"array"}) || !reflect.DeepEqual(saved, got) {
		t.Fatalf("unexpected record: got=%+v saved=%+v", got, saved)
	}
}

func TestCreateHandlerRejectsInvalidRequest(t *testing.T) {
	service := question.NewQuestionService(creatorFunc(func(context.Context, question.QuestionRecord) error {
		t.Fatal("invalid request reached repository")
		return nil
	}))
	for _, body := range []string{`{"title":`, `{"title":"","type":"algorithm","bodyMarkdown":"body"}`, `{"title":"title","type":"algorithm","bodyMarkdown":"body","unexpected":true}`} {
		response := httptest.NewRecorder()
		CreateHandler(service).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/questions", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected status 400, got %d", body, response.Code)
		}
	}
}

func TestRegisterRoutesGetsQuestion(t *testing.T) {
	service := question.NewQuestionServiceWithDependencies(question.QuestionServiceDependencies{DetailReader: detailReaderFunc(func(_ context.Context, id string) (question.QuestionDetail, error) {
		if id == "missing" {
			return question.QuestionDetail{}, question.ErrNotFound
		}
		return question.QuestionDetail{Question: question.QuestionRecord{ID: id, Title: "Two Sum", Type: question.QuestionTypeAlgorithm, BodyMarkdown: "body"}}, nil
	})})
	router := chi.NewRouter()
	RegisterRoutes(router, service)
	for _, tc := range []struct {
		id     string
		status int
	}{{"q1", http.StatusOK}, {"missing", http.StatusNotFound}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/questions/"+tc.id, nil))
		if response.Code != tc.status {
			t.Fatalf("id %s: expected status %d, got %d: %s", tc.id, tc.status, response.Code, response.Body.String())
		}
	}
}
