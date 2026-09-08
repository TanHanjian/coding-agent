package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const (
	CodeInvalidRequest     = "invalid_request"
	CodeNotFound           = "not_found"
	CodeStorageUnavailable = "storage_unavailable"
	CodeMigrationFailed    = "migration_failed"
	CodeInternal           = "internal_error"
)

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	id := RequestID(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(ErrorBody{Error: ErrorDetail{Code: code, Message: message, RequestID: id}}); err != nil {
		slog.Error("write error response", "request_id", id, "error", err)
	}
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("http panic", "request_id", RequestID(r.Context()))
				WriteError(w, r, http.StatusInternalServerError, CodeInternal, "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
