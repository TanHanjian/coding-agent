package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/agent/interview"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/storage"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func TestHealthHandler(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "data", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db); err != nil {
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

func TestNewChatExecutorRequiresProjectionReader(t *testing.T) {
	cfg := config.Config{OpenAI: config.OpenAIConfig{APIKey: "test-key", Model: "test-model"}}
	_, err := newChatExecutor(t.Context(), cfg, interview.ToolDependencies{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "projection reader is required") {
		t.Fatalf("missing reader error = %v", err)
	}
}

func TestNewChatExecutorRequiresChatConfiguration(t *testing.T) {
	_, err := newChatExecutor(t.Context(), config.Config{}, interview.ToolDependencies{}, nil, nil)
	if err == nil {
		t.Fatal("expected missing chat configuration error")
	}
}
