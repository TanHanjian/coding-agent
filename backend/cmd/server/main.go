package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	"interview-memory-agent/backend/internal/agent/eino"
	"interview-memory-agent/backend/internal/agent/interview"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/domain/review"
	agentopenai "interview-memory-agent/backend/internal/infrastructure/agent/openai"
	"interview-memory-agent/backend/internal/infrastructure/config"
	cozeloopinfra "interview-memory-agent/backend/internal/infrastructure/cozeloop"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
	"interview-memory-agent/backend/internal/infrastructure/storage"

	einoschema "github.com/cloudwego/eino/schema"
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
	if err := storage.Migrate(context.Background(), db); err != nil {
		slog.Error("run migrations", "error", err)
		os.Exit(1)
	}
	cozeLoopClient, err := cozeloopinfra.New(cfg.CozeLoop)
	if err != nil {
		slog.Error("configure CozeLoop", "error", err)
		os.Exit(1)
	}
	if err := cozeLoopClient.RegisterEinoTracing(); err != nil {
		slog.Error("register CozeLoop tracing", "error", err)
		closeCozeLoop(cozeLoopClient)
		os.Exit(1)
	}
	defer closeCozeLoop(cozeLoopClient)
	promptProvider, err := configurePromptProvider(cozeLoopClient, cfg.CozeLoop)
	if err != nil {
		slog.Error("configure prompt provider", "error", err)
		closeCozeLoop(cozeLoopClient)
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
	agentRunStore, err := sqlite.NewAgentRunRepository(db)
	if err != nil {
		slog.Error("create agent run store", "error", err)
		os.Exit(1)
	}
	chatExecutor, err := newChatExecutor(context.Background(), cfg, interview.ToolDependencies{
		QuestionSearcher:      questionService,
		QuestionContextReader: questionService,
	}, agentRunStore, agentRunStore.GetEntryTree(), promptProvider)
	if err != nil {
		slog.Error("configure chat executor", "error", err)
		closeCozeLoop(cozeLoopClient)
		os.Exit(1)
	}
	// Recover any interrupted runs from previous crashes
	recoveryResult, err := agentRunStore.RecoverInterrupted(context.Background())
	if err != nil {
		slog.Error("recover interrupted runs", "error", err)
		os.Exit(1)
	}
	if recoveryResult.InterruptedRuns > 0 || recoveryResult.LegacyOnlyMessages > 0 {
		slog.Info("recovered interrupted state",
			"interrupted_runs", recoveryResult.InterruptedRuns,
			"legacy_only_messages", recoveryResult.LegacyOnlyMessages)
	}

	chatService, err := chat.NewRunService(chat.RunDependencies{
		Executor:    chatExecutor,
		Registry:    chat.NewMemoryGenerationRegistry(),
		Hub:         chat.NewMemoryGenerationHub(),
		RunContexts: chat.NewDetachedRunContextFactory(context.Background(), 2*time.Minute),
		Store:       agentRunStore,
	})
	if err != nil {
		slog.Error("configure chat service", "error", err)
		closeCozeLoop(cozeLoopClient)
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
		closeCozeLoop(cozeLoopClient)
		os.Exit(1)
	}
}

func closeCozeLoop(client *cozeloopinfra.Client) {
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Close(closeContext); err != nil {
		slog.Error("close CozeLoop", "error", err)
	}
}

// newChatExecutor 仅负责应用装配：把进程级模型配置、面试 Graph Builder 和
// 应用层 Eino Executor 连接起来。提示词、工具、检索和 Graph 策略仍归 Builder 所有。
func newChatExecutor(ctx context.Context, cfg config.Config, toolDeps interview.ToolDependencies, projectionReader conversation.ProjectionReader, entryTree conversation.EntryTree, promptProviders ...agentprompt.Provider) (chat.Executor, error) {
	if err := cfg.ValidateForChat(); err != nil {
		return nil, fmt.Errorf("validate chat configuration: %w", err)
	}
	if projectionReader == nil {
		return nil, fmt.Errorf("configure chat context: projection reader is required")
	}
	if entryTree == nil {
		return nil, fmt.Errorf("configure chat context: entry tree is required")
	}
	chatModel, err := agentopenai.NewChatModel(ctx, cfg.OpenAI)
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	tools, err := interview.NewTools(toolDeps)
	if err != nil {
		return nil, fmt.Errorf("create interview tools: %w", err)
	}
	promptProvider := agentprompt.Provider(agentprompt.NewLocalPromptProvider())
	if len(promptProviders) > 0 && promptProviders[0] != nil {
		promptProvider = promptProviders[0]
	}
	runtimeBuilder, err := interview.NewBuilderWithPromptProvider(chatModel, promptProvider, tools...)
	if err != nil {
		return nil, fmt.Errorf("create interview runtime builder: %w", err)
	}

	// Phase 4: 创建 Context Compaction 组件
	// 1. 预算策略：配置模型窗口大小
	modelWindows := map[string]int{
		cfg.OpenAI.Model: 128000, // 默认值，实际应从模型配置读取
		"default":        128000,
	}
	budgetConfig := agentcontext.DefaultBudgetConfig()
	budgetPolicy := agentcontext.NewEnforcedBudgetPolicy(budgetConfig, modelWindows)

	// 2. Token 估算器：字符/token 比例 4.0
	tokenEstimator := agentcontext.NewCharBasedTokenEstimator(4.0)

	// 3. 边界选择器
	boundarySelector := agentcontext.NewDefaultBoundarySelector(tokenEstimator)

	// 4. 摘要生成器：包装 ChatModel 为函数式接口
	modelInvoker := func(ctx context.Context, messages []*einoschema.Message) (*einoschema.Message, error) {
		resp, err := chatModel.Generate(ctx, messages)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
	contextSummarizer := agentcontext.NewEinoContextSummarizer(modelInvoker)

	// 5. 摘要协调器
	summaryCoordinator := agentcontext.NewDefaultSummaryCoordinator(contextSummarizer, tokenEstimator)

	// 6. Compaction 适配器
	compactionAdapter := sqlite.NewCompactionAdapter(entryTree)

	chatExecutor, err := eino.NewExecutor(
		runtimeBuilder,
		eino.WithContextManager(agentcontext.NewManager(
			agentcontext.WithProjectionReader(projectionReader),
			agentcontext.WithBudgetPolicy(budgetPolicy),
			agentcontext.WithTokenEstimator(tokenEstimator),
			agentcontext.WithBoundarySelector(boundarySelector),
			agentcontext.WithSummaryCoordinator(summaryCoordinator),
			agentcontext.WithCompactionWriter(compactionAdapter),
			agentcontext.WithCompactionReader(compactionAdapter),
		)),
		eino.WithGraphDebugLogging(cfg.AgentDebug),
	)
	if err != nil {
		return nil, fmt.Errorf("create chat executor: %w", err)
	}
	return chatExecutor, nil
}

func configurePromptProvider(client *cozeloopinfra.Client, cfg config.CozeLoopConfig) (agentprompt.Provider, error) {
	fallback := agentprompt.NewLocalPromptProvider()
	if !cfg.PromptEnabled {
		return fallback, nil
	}
	return cozeloopinfra.NewPromptProvider(client, fallback)
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
