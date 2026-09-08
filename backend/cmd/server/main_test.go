package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"interview-memory-agent/backend/internal/infrastructure/storage"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func TestHealthHandler(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "data", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	handler := httpx.WithRequestID(healthHandler(db))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if response.Header().Get(httpx.RequestIDHeader) == "" {
		t.Fatal("expected request ID")
	}
}
