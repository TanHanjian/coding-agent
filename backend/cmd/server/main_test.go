package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	"interview-memory-agent/backend/internal/agent/interview"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/repository/sqlite"
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
	cfg := validServerChatConfig()
	_, err := newChatExecutor(t.Context(), cfg, interview.ToolDependencies{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "projection reader is required") {
		t.Fatalf("missing reader error = %v", err)
	}
}

func TestNewChatExecutorRejectsInvalidChatBudget(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*config.OpenAIConfig)
		field  string
	}{
		{"missing credentials", func(c *config.OpenAIConfig) { c.APIKey = "" }, "OPENAI_API_KEY"},
		{"missing window", func(c *config.OpenAIConfig) { c.ContextWindowTokens = 0 }, "OPENAI_CONTEXT_WINDOW_TOKENS"},
		{"missing output", func(c *config.OpenAIConfig) { c.MaxOutputTokens = 0 }, "OPENAI_MAX_OUTPUT_TOKENS"},
		{"missing margin", func(c *config.OpenAIConfig) { c.SafetyMarginConfigured = false }, "OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS"},
		{"negative margin", func(c *config.OpenAIConfig) { c.SafetyMarginTokens = -1 }, "OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS"},
		{"no input capacity", func(c *config.OpenAIConfig) { c.MaxOutputTokens = c.ContextWindowTokens - c.SafetyMarginTokens }, "OPENAI_MAX_OUTPUT_TOKENS"},
		{"missing output mode", func(c *config.OpenAIConfig) { c.OutputLimitMode = "" }, "OPENAI_OUTPUT_LIMIT_MODE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := validServerChatConfig()
			test.mutate(&cfg.OpenAI)
			executor, err := newChatExecutor(t.Context(), cfg, interview.ToolDependencies{}, nil, nil)
			if executor != nil || err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("executor = %v, error = %v; want rejection for %s", executor, err, test.field)
			}
			for _, secret := range []string{cfg.OpenAI.APIKey, cfg.OpenAI.Model} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatalf("configuration error leaked a configured value: %v", err)
				}
			}
		})
	}
}

func TestNewChatExecutorWithoutModelStaysOffline(t *testing.T) {
	for _, model := range []string{"", "   "} {
		cfg := config.Config{OpenAI: config.OpenAIConfig{APIKey: "unused-key", Model: model}}
		executor, err := newChatExecutor(t.Context(), cfg, interview.ToolDependencies{}, nil, nil)
		if err != nil || executor != nil {
			t.Fatalf("offline executor = %v, error = %v", executor, err)
		}
	}
}

func TestNewChatExecutorAssemblesValidatedBudgetWithoutModelCalls(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	db, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.NewAgentRunRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	cfg := validServerChatConfig()
	cfg.OpenAI.BaseURL = server.URL + "/v1"
	tools := interview.ToolDependencies{QuestionSearcher: serverQuestionTools{}, QuestionContextReader: serverQuestionTools{}}
	executor, err := newChatExecutor(t.Context(), cfg, tools, store, store.GetEntryTree())
	if err != nil || executor == nil {
		t.Fatalf("executor = %v, error = %v", executor, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("assembly made %d model calls", requests.Load())
	}
}

func TestPreparationBudgetPolicyUsesConfiguredCapacity(t *testing.T) {
	for _, window := range []int{1000, 64000} {
		callConfig := agentcontext.ModelCallConfig{ModelID: "custom-model", ContextWindowTokens: window, OutputLimitTokens: 100, SafetyMarginTokens: 37}
		policy, err := newPreparationBudgetPolicy(callConfig)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := policy.Plan(t.Context(), chat.ContextInput{})
		if err != nil {
			t.Fatal(err)
		}
		sections := plan.InstructionTokens + plan.SummaryTokens + plan.HistoryTokens + plan.InterviewMaterialTokens + plan.ToolResultTokens
		if plan.ContextWindowTokens != window || plan.OutputReservedTokens != 100 || sections != window-100-37 {
			t.Fatalf("plan = %+v, section sum = %d", plan, sections)
		}
		if plan.HistoryTokens <= 0 || plan.SummaryTokens <= 0 {
			t.Fatalf("missing preparation sections: %+v", plan)
		}
	}
	if _, err := newPreparationBudgetPolicy(agentcontext.ModelCallConfig{}); err == nil {
		t.Fatal("invalid preparation configuration accepted")
	}
}

func validServerChatConfig() config.Config {
	return config.Config{OpenAI: config.OpenAIConfig{
		APIKey: "test-key", Model: "test-model",
		ContextWindowTokens: 16000, MaxOutputTokens: 1000,
		SafetyMarginTokens: 100, SafetyMarginConfigured: true,
		OutputLimitMode: "max_tokens",
	}}
}

type serverQuestionTools struct{}

func (serverQuestionTools) Search(context.Context, question.QuestionSearchQuery) (question.QuestionSearchResult, error) {
	return question.QuestionSearchResult{}, nil
}

func (serverQuestionTools) Get(context.Context, string) (question.QuestionDetail, error) {
	return question.QuestionDetail{}, nil
}

var _ conversation.ProjectionReader = (*sqlite.AgentRunRepository)(nil)
