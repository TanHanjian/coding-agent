package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/domain/review"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
	"interview-memory-agent/backend/internal/transport/httpx"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	paths := storage.NewPaths(cfg.DataDir)
	if err := paths.Ensure(); err != nil {
		slog.Error("prepare data directories", "error", err)
		os.Exit(1)
	}
	db, err := storage.Open(context.Background(), paths.Database)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := storage.Migrate(context.Background(), db.DB); err != nil {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}
	questionRepository := sqlite.NewQuestionRepository(db)
	answerRepository := sqlite.NewAnswerRepository(db)
	reviewRepository := sqlite.NewReviewRepository(db)
	questionService := question.NewQuestionServiceWithDependencies(question.QuestionServiceDependencies{
		Creator:       questionRepository,
		DetailReader:  questionRepository,
		Updater:       questionRepository,
		ArchiveWriter: questionRepository,
		Deleter:       questionRepository,
		Searcher:      questionRepository,
	})
	answerService := answer.NewService(answer.Dependencies{Store: answerRepository, Questions: questionRepository})
	reviewService := review.NewService(review.Dependencies{Store: reviewRepository, Answers: answerRepository, Questions: questionRepository})
	router := chi.NewRouter()
	router.Get("/healthz", healthHandler(db).ServeHTTP)
	router.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler(db).ServeHTTP)
		question.RegisterRoutes(r, questionService)
		answer.RegisterRoutes(r, answerService)
		review.RegisterRoutes(r, reviewService)
	})
	handler := httpx.WithRequestID(httpx.Recover(requestLogger(router)))
	server := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("starting backend", "addr", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func healthHandler(db *storage.DB) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := db.PingContext(r.Context()); err != nil {
			httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeStorageUnavailable, "本地数据暂时不可用")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"interview-memory-agent"}` + "\n"))
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("http request", "request_id", httpx.RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}
