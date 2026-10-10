package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/infrastructure/config"

	"github.com/cloudwego/eino/schema"
)

func budgetConfig() config.OpenAIConfig {
	return config.OpenAIConfig{
		APIKey:                 "test-key",
		Model:                  "test-model",
		ContextWindowTokens:    1000,
		MaxOutputTokens:        100,
		SafetyMarginTokens:     0,
		SafetyMarginConfigured: true,
		OutputLimitMode:        "max_tokens",
	}
}

func TestNewChatModelOutputLimitRequests(t *testing.T) {
	for _, mode := range []string{"max_tokens", "max_completion_tokens", ""} {
		for _, streaming := range []bool{false, true} {
			name := mode
			if name == "" {
				name = "independent judge without budget"
			}
			if streaming {
				name += "/stream"
			} else {
				name += "/generate"
			}
			t.Run(name, func(t *testing.T) {
				requests := make(chan map[string]json.RawMessage, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
						http.Error(w, "invalid JSON", http.StatusBadRequest)
						return
					}
					requests <- body
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"local\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"local\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":"local","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()

				cfg := budgetConfig()
				cfg.OutputLimitMode = mode
				if mode == "" {
					cfg = config.OpenAIConfig{APIKey: "judge-key", Model: "test-model"}
				}
				cfg.BaseURL = server.URL + "/v1"
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				chatModel, err := NewChatModel(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				messages := []*schema.Message{schema.UserMessage("local test")}
				if streaming {
					reader, err := chatModel.Stream(ctx, messages)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					content := ""
					for {
						message, err := reader.Recv()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						content += message.Content
					}
					if content != "ok" {
						t.Fatalf("stream content = %q", content)
					}
				} else {
					message, err := chatModel.Generate(ctx, messages)
					if err != nil {
						t.Fatal(err)
					}
					if message.Content != "ok" {
						t.Fatalf("generated content = %q", message.Content)
					}
				}

				var body map[string]json.RawMessage
				select {
				case body = <-requests:
				default:
					t.Fatal("expected a local HTTP request")
				}
				for _, field := range []string{"max_tokens", "max_completion_tokens"} {
					value, present := body[field]
					if field == mode {
						var cap int
						if !present || json.Unmarshal(value, &cap) != nil || cap != cfg.MaxOutputTokens {
							t.Fatalf("%s = %s, want output reserve %d", field, value, cfg.MaxOutputTokens)
						}
					} else if present {
						t.Fatalf("unexpected output field %s = %s", field, value)
					}
				}
				var sentModel string
				if err := json.Unmarshal(body["model"], &sentModel); err != nil || sentModel != cfg.Model {
					t.Fatalf("model = %s, want configured model", body["model"])
				}
			})
		}
	}
}

func TestNewChatModelRejectsPartialOrInvalidBudget(t *testing.T) {
	tests := []struct {
		name   string
		config config.OpenAIConfig
	}{
		{name: "window only", config: config.OpenAIConfig{ContextWindowTokens: 1000}},
		{name: "output only", config: config.OpenAIConfig{MaxOutputTokens: 100}},
		{name: "margin only", config: config.OpenAIConfig{SafetyMarginTokens: 20}},
		{name: "explicit zero margin only", config: config.OpenAIConfig{SafetyMarginConfigured: true}},
		{name: "mode only", config: config.OpenAIConfig{OutputLimitMode: "max_tokens"}},
		{name: "negative window", config: config.OpenAIConfig{ContextWindowTokens: -1}},
		{name: "negative output", config: config.OpenAIConfig{MaxOutputTokens: -1}},
		{name: "negative margin", config: config.OpenAIConfig{SafetyMarginTokens: -1}},
	}
	invalidMode := budgetConfig()
	invalidMode.OutputLimitMode = "unsupported"
	tests = append(tests, struct {
		name   string
		config config.OpenAIConfig
	}{name: "invalid mode", config: invalidMode})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "unexpected request", http.StatusInternalServerError)
			}))
			defer server.Close()
			cfg := tt.config
			cfg.APIKey, cfg.Model, cfg.BaseURL = "test-key", "test-model", server.URL+"/v1"
			if err := ValidateConfig(cfg); err == nil {
				t.Fatal("partial/invalid budget must not validate")
			}
			chatModel, err := NewChatModel(t.Context(), cfg)
			if err == nil || chatModel != nil {
				t.Fatalf("invalid budget created a model: model=%v, err=%v", chatModel, err)
			}
			if calls.Load() != 0 {
				t.Fatal("invalid config caused an HTTP request")
			}
		})
	}
}
