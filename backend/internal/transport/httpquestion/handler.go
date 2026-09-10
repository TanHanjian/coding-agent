// Package httpquestion 提供 Question 用例的 HTTP 传输适配器。
package httpquestion

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	question "interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func RegisterRoutes(r chi.Router, service question.Service) {
	r.Post("/questions", CreateHandler(service))
	r.Get("/questions", SearchHandler(service))
	r.Get("/questions/{questionID}", GetHandler(service))
	r.Patch("/questions/{questionID}", UpdateHandler(service))
	r.Post("/questions/{questionID}/archive", ArchiveHandler(service))
	r.Post("/questions/{questionID}/restore", RestoreHandler(service))
	r.Delete("/questions/{questionID}", DeleteHandler(service))
}

// Deprecated: use RegisterRoutes.
func RegisterQuestionRoutes(r chi.Router, service question.Service) { RegisterRoutes(r, service) }

func CreateHandler(service question.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input question.CreateQuestionInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r, "请求体无效")
			return
		}
		record, err := service.Create(r.Context(), input)
		if err != nil {
			domainError(w, r, err, "创建题目失败")
			return
		}
		writeJSON(w, http.StatusCreated, record)
	}
}
func GetHandler(service question.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		detail, err := service.Get(r.Context(), chi.URLParam(r, "questionID"))
		if err != nil {
			domainError(w, r, err, "读取题目失败")
			return
		}
		writeJSON(w, http.StatusOK, detail)
	}
}
func UpdateHandler(service question.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input question.UpdateQuestionInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r, "请求体无效")
			return
		}
		record, err := service.Update(r.Context(), chi.URLParam(r, "questionID"), input)
		if err != nil {
			domainError(w, r, err, "更新题目失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}
func SearchHandler(service question.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query, err := searchQuery(r)
		if err != nil {
			invalid(w, r, "查询参数无效")
			return
		}
		result, err := service.Search(r.Context(), query)
		if err != nil {
			domainError(w, r, err, "搜索题目失败")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
func ArchiveHandler(service question.Service) http.HandlerFunc { return archiveHandler(service, true) }
func RestoreHandler(service question.Service) http.HandlerFunc { return archiveHandler(service, false) }
func archiveHandler(service question.Service, archived bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var err error
		if archived {
			err = service.Archive(r.Context(), chi.URLParam(r, "questionID"))
		} else {
			err = service.Restore(r.Context(), chi.URLParam(r, "questionID"))
		}
		if err != nil {
			domainError(w, r, err, "更新题目状态失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func DeleteHandler(service question.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.DeletePermanent(r.Context(), chi.URLParam(r, "questionID")); err != nil {
			domainError(w, r, err, "删除题目失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
func searchQuery(r *http.Request) (question.QuestionSearchQuery, error) {
	values := r.URL.Query()
	query := question.QuestionSearchQuery{Text: values.Get("text"), Source: values.Get("source"), SortBy: values.Get("sortBy"), SortDirection: values.Get("sortDirection"), Tags: values["tag"]}
	for _, v := range values["type"] {
		query.Types = append(query.Types, question.QuestionType(v))
	}
	for _, v := range values["difficulty"] {
		query.Difficulties = append(query.Difficulties, question.Difficulty(v))
	}
	for _, v := range values["result"] {
		query.Results = append(query.Results, question.AnswerResult(v))
	}
	if v := values.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return question.QuestionSearchQuery{}, err
		}
		query.Page = n
	}
	if v := values.Get("pageSize"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return question.QuestionSearchQuery{}, err
		}
		query.PageSize = n
	}
	for _, field := range []struct {
		name   string
		target **bool
	}{{"archived", &query.Archived}, {"hasMistakes", &query.HasMistakes}} {
		if v := values.Get(field.name); v != "" {
			parsed, err := strconv.ParseBool(v)
			if err != nil {
				return question.QuestionSearchQuery{}, err
			}
			*field.target = &parsed
		}
	}
	return query, nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func invalid(w http.ResponseWriter, r *http.Request, message string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, message)
}
func domainError(w http.ResponseWriter, r *http.Request, err error, message string) {
	switch {
	case errors.Is(err, question.ErrInvalidInput):
		invalid(w, r, message)
	case errors.Is(err, question.ErrNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, message)
	default:
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, message)
	}
}

// Deprecated handler names retained for existing callers.
func CreateQuestionHandler(s question.Service) http.HandlerFunc  { return CreateHandler(s) }
func GetQuestionHandler(s question.Service) http.HandlerFunc     { return GetHandler(s) }
func UpdateQuestionHandler(s question.Service) http.HandlerFunc  { return UpdateHandler(s) }
func SearchQuestionsHandler(s question.Service) http.HandlerFunc { return SearchHandler(s) }
func ArchiveQuestionHandler(s question.Service) http.HandlerFunc { return ArchiveHandler(s) }
func RestoreQuestionHandler(s question.Service) http.HandlerFunc { return RestoreHandler(s) }
func DeleteQuestionHandler(s question.Service) http.HandlerFunc  { return DeleteHandler(s) }
