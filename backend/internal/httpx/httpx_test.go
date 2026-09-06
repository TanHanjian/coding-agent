package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithRequestIDPreservesAndGenerates(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := WithRequestID(inner)
	generated := httptest.NewRecorder()
	handler.ServeHTTP(generated, httptest.NewRequest(http.MethodGet, "/", nil))
	if generated.Header().Get(RequestIDHeader) == "" {
		t.Fatal("expected generated request ID")
	}
	preserved := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "req-test")
	handler.ServeHTTP(preserved, request)
	if got := preserved.Header().Get(RequestIDHeader); got != "req-test" {
		t.Fatalf("expected preserved request ID, got %q", got)
	}
}

func TestWriteErrorShape(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusBadRequest, CodeInvalidRequest, "请求无效")
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "req-test")
	response := httptest.NewRecorder()
	WithRequestID(inner).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if response.Body.String() != `{"error":{"code":"invalid_request","message":"请求无效","requestId":"req-test"}}
` {
		t.Fatalf("unexpected error response: %s", response.Body.String())
	}
}
