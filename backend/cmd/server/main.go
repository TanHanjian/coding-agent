package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"interview-memory-agent/backend/internal/agent/eino"
	"interview-memory-agent/backend/internal/agent/interview"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/domain/review"
	agentopenai "interview-memory-agent/backend/internal/infrastructure/agent/openai"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"
	"interview-memory-agent/backend/internal/transport/httpanswer"
	"interview-memory-agent/backend/internal/transport/httpchat"
	"interview-memory-agent/backend/internal/transport/httpconversation"
	"interview-memory-agent/backend/internal/transport/httpquestion"
	"interview-memory-agent/backend/internal/transport/httpreview"
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
	chatExecutor, err := newChatExecutor(context.Background(), cfg)
	if err != nil {
		slog.Error("configure chat executor", "error", err)
		os.Exit(1)
	}
	questionRepository := sqlite.NewQuestionRepository(db)
	answerRepository := sqlite.NewAnswerRepository(db)
	reviewRepository := sqlite.NewReviewRepository(db)
	conversationRepository := sqlite.NewConversationRepository(db)
	messageRepository := sqlite.NewMessageRepository(db)
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
	conversationService := conversation.NewService(conversation.Dependencies{Conversations: conversationRepository, Messages: messageRepository})
	chatService, err := chat.NewSkeletonService(chat.Dependencies{
		Executor:    chatExecutor,
		Registry:    chat.NewMemoryGenerationRegistry(),
		Hub:         chat.NewMemoryGenerationHub(),
		RunContexts: chat.NewDetachedRunContextFactory(context.Background(), 2*time.Minute),
		Store:       sqlite.NewChatTurnRepository(db),
	})
	if err != nil {
		slog.Error("configure chat service", "error", err)
		os.Exit(1)
	}
	router := chi.NewRouter()
	router.Get("/healthz", healthHandler(db).ServeHTTP)
	router.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler(db).ServeHTTP)
		httpquestion.RegisterRoutes(r, questionService)
		httpanswer.RegisterRoutes(r, answerService)
		httpreview.RegisterRoutes(r, reviewService)
		httpconversation.RegisterRoutes(r, conversationService)
		httpchat.RegisterRoutes(r, chatService)
	})
	handler := httpx.WithRequestID(httpx.Recover(requestLogger(router)))
	server := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("starting backend", "addr", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// newChatExecutor 仅负责应用装配：把进程级模型配置、面试 Graph Builder 和
// 应用层 Eino Executor 连接起来。提示词、工具、检索和 Graph 策略仍归 Builder 所有。
func newChatExecutor(ctx context.Context, cfg config.Config) (chat.Executor, error) {
	if err := cfg.ValidateForChat(); err != nil {
		return nil, fmt.Errorf("validate chat configuration: %w", err)
	}
	chatModel, err := agentopenai.NewChatModel(ctx, cfg.OpenAI)
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	runtimeBuilder, err := interview.NewBuilder(chatModel)
	if err != nil {
		return nil, fmt.Errorf("create interview runtime builder: %w", err)
	}
	chatExecutor, err := eino.NewExecutor(runtimeBuilder)
	if err != nil {
		return nil, fmt.Errorf("create chat executor: %w", err)
	}
	return chatExecutor, nil
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
