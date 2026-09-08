package review

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"interview-memory-agent/backend/internal/domain/domainerr"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func RegisterRoutes(r chi.Router, service Service) {
	r.Post("/questions/{questionID}/reviews", CreateHandler(service))
	r.Get("/questions/{questionID}/reviews", ListHandler(service))
	r.Patch("/reviews/{reviewID}", UpdateHandler(service))
	r.Delete("/reviews/{reviewID}", DeleteHandler(service))
}
func CreateHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input CreateInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.Create(r.Context(), chi.URLParam(r, "questionID"), input)
		if err != nil {
			domainError(w, r, err, "创建复盘失败")
			return
		}
		writeJSON(w, http.StatusCreated, record)
	}
}
func ListHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := service.List(r.Context(), chi.URLParam(r, "questionID"))
		if err != nil {
			domainError(w, r, err, "读取复盘失败")
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}
func UpdateHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input UpdateInput
		if err := decodeJSON(r, &input); err != nil {
			invalid(w, r)
			return
		}
		record, err := service.Update(r.Context(), chi.URLParam(r, "reviewID"), input)
		if err != nil {
			domainError(w, r, err, "更新复盘失败")
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}
func DeleteHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.Delete(r.Context(), chi.URLParam(r, "reviewID")); err != nil {
			domainError(w, r, err, "删除复盘失败")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func decodeJSON(r *http.Request, target any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func invalid(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, "请求体无效")
}
func domainError(w http.ResponseWriter, r *http.Request, err error, message string) {
	if errors.Is(err, domainerr.ErrInvalidInput) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInvalidRequest, message)
	} else if errors.Is(err, domainerr.ErrNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, message)
	} else {
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, message)
	}
}
