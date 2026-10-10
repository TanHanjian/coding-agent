package config

import (
	"strconv"
	"strings"
	"testing"
)

func validBudgetConfig() OpenAIConfig {
	return OpenAIConfig{
		ContextWindowTokens:    1000,
		MaxOutputTokens:        100,
		SafetyMarginTokens:     20,
		SafetyMarginConfigured: true,
		OutputLimitMode:        "max_tokens",
	}
}

func clearBudgetEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OPENAI_CONTEXT_WINDOW_TOKENS", "OPENAI_MAX_OUTPUT_TOKENS",
		"OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS", "OPENAI_OUTPUT_LIMIT_MODE",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadBudgetMissingAndExplicitZero(t *testing.T) {
	t.Chdir(t.TempDir()) // Do not read a developer's .env.
	clearBudgetEnv(t)
	t.Setenv("APP_DATA_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenAI.HasBudget() || cfg.OpenAI.ContextWindowTokens != 0 || cfg.OpenAI.MaxOutputTokens != 0 || cfg.OpenAI.SafetyMarginTokens != 0 || cfg.OpenAI.OutputLimitMode != "" {
		t.Fatalf("missing budget must not acquire defaults: %+v", cfg.OpenAI)
	}
	if err := cfg.OpenAI.ValidateBudget(); err == nil {
		t.Fatal("missing budget must not validate")
	}

	t.Setenv("OPENAI_CONTEXT_WINDOW_TOKENS", " 1000 ")
	t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", " 100 ")
	t.Setenv("OPENAI_OUTPUT_LIMIT_MODE", " max_completion_tokens ")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OpenAI.SafetyMarginConfigured || cfg.OpenAI.ValidateBudget() == nil {
		t.Fatal("absent safety margin must not validate")
	}

	t.Setenv("OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS", " 0 ")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OpenAI.SafetyMarginConfigured || cfg.OpenAI.SafetyMarginTokens != 0 {
		t.Fatalf("explicit zero margin lost: %+v", cfg.OpenAI)
	}
	if err := cfg.OpenAI.ValidateBudget(); err != nil {
		t.Fatalf("explicit zero margin should validate: %v", err)
	}
}

func TestLoadRejectsInvalidBudgetValuesWithoutLeaking(t *testing.T) {
	for _, key := range []string{
		"OPENAI_CONTEXT_WINDOW_TOKENS", "OPENAI_MAX_OUTPUT_TOKENS",
		"OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS",
	} {
		for _, value := range []string{"private-invalid-value", "1.5", "9999999999999999999999999999999999999999", "-987654321"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Chdir(t.TempDir())
				clearBudgetEnv(t)
				t.Setenv("APP_DATA_DIR", t.TempDir())
				t.Setenv(key, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("want field-specific parse error, got %v", err)
				}
				if strings.Contains(err.Error(), value) {
					t.Fatalf("parse error leaks configuration value: %v", err)
				}
			})
		}
	}
	for _, key := range []string{"OPENAI_CONTEXT_WINDOW_TOKENS", "OPENAI_MAX_OUTPUT_TOKENS"} {
		t.Run(key+"/zero", func(t *testing.T) {
			t.Chdir(t.TempDir())
			clearBudgetEnv(t)
			t.Setenv("APP_DATA_DIR", t.TempDir())
			t.Setenv(key, "0")
			if _, err := Load(); err == nil {
				t.Fatal("explicit zero window/output must fail")
			}
		})
	}
	t.Run("invalid mode", func(t *testing.T) {
		t.Chdir(t.TempDir())
		clearBudgetEnv(t)
		t.Setenv("APP_DATA_DIR", t.TempDir())
		t.Setenv("OPENAI_OUTPUT_LIMIT_MODE", "private-invalid-mode")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "OPENAI_OUTPUT_LIMIT_MODE") || strings.Contains(err.Error(), "private-invalid-mode") {
			t.Fatalf("want safe mode error, got %v", err)
		}
	})
}

func TestValidateBudget(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name    string
		change  func(*OpenAIConfig)
		wantErr bool
	}{
		{name: "valid max_tokens"},
		{name: "valid max_completion_tokens", change: func(c *OpenAIConfig) { c.OutputLimitMode = "max_completion_tokens" }},
		{name: "explicit zero margin", change: func(c *OpenAIConfig) { c.SafetyMarginTokens = 0 }},
		{name: "missing window", change: func(c *OpenAIConfig) { c.ContextWindowTokens = 0 }, wantErr: true},
		{name: "negative window", change: func(c *OpenAIConfig) { c.ContextWindowTokens = -1 }, wantErr: true},
		{name: "missing output", change: func(c *OpenAIConfig) { c.MaxOutputTokens = 0 }, wantErr: true},
		{name: "negative output", change: func(c *OpenAIConfig) { c.MaxOutputTokens = -1 }, wantErr: true},
		{name: "missing explicit margin", change: func(c *OpenAIConfig) { c.SafetyMarginConfigured = false }, wantErr: true},
		{name: "negative margin", change: func(c *OpenAIConfig) { c.SafetyMarginTokens = -1 }, wantErr: true},
		{name: "missing mode", change: func(c *OpenAIConfig) { c.OutputLimitMode = "" }, wantErr: true},
		{name: "unsupported mode", change: func(c *OpenAIConfig) { c.OutputLimitMode = "secret-mode" }, wantErr: true},
		{name: "mode is case sensitive", change: func(c *OpenAIConfig) { c.OutputLimitMode = "MAX_TOKENS" }, wantErr: true},
		{name: "one input token remains", change: func(c *OpenAIConfig) { c.ContextWindowTokens = 121 }},
		{name: "equal window", change: func(c *OpenAIConfig) { c.ContextWindowTokens = 120 }, wantErr: true},
		{name: "above window", change: func(c *OpenAIConfig) { c.ContextWindowTokens = 119 }, wantErr: true},
		{name: "output equals window", change: func(c *OpenAIConfig) { c.MaxOutputTokens = 1000; c.SafetyMarginTokens = 0 }, wantErr: true},
		{name: "output above window", change: func(c *OpenAIConfig) { c.MaxOutputTokens = 1001 }, wantErr: true},
		{name: "max int valid", change: func(c *OpenAIConfig) {
			c.ContextWindowTokens = maxInt
			c.MaxOutputTokens = maxInt - 1
			c.SafetyMarginTokens = 0
		}},
		{name: "max int equals window", change: func(c *OpenAIConfig) {
			c.ContextWindowTokens = maxInt
			c.MaxOutputTokens = maxInt - 1
			c.SafetyMarginTokens = 1
		}, wantErr: true},
		{name: "sum would overflow", change: func(c *OpenAIConfig) {
			c.ContextWindowTokens = maxInt
			c.MaxOutputTokens = maxInt - 1
			c.SafetyMarginTokens = maxInt
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBudgetConfig()
			if tt.change != nil {
				tt.change(&cfg)
			}
			err := cfg.ValidateBudget()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateBudget() = %v, wantErr %v", err, tt.wantErr)
			}
			cfg.APIKey, cfg.Model = "private-key", "private-model"
			err = (Config{OpenAI: cfg}).ValidateForChat()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateForChat() = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && (strings.Contains(err.Error(), cfg.APIKey) || strings.Contains(err.Error(), cfg.Model) || strings.Contains(err.Error(), "secret-mode")) {
				t.Fatalf("validation leaks configuration values: %v", err)
			}
		})
	}
}

func TestLoadBudgetMaxInt(t *testing.T) {
	t.Chdir(t.TempDir())
	clearBudgetEnv(t)
	t.Setenv("APP_DATA_DIR", t.TempDir())
	maxInt := int(^uint(0) >> 1)
	t.Setenv("OPENAI_CONTEXT_WINDOW_TOKENS", strconv.Itoa(maxInt))
	t.Setenv("OPENAI_MAX_OUTPUT_TOKENS", strconv.Itoa(maxInt-1))
	t.Setenv("OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS", "0")
	t.Setenv("OPENAI_OUTPUT_LIMIT_MODE", "max_tokens")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.OpenAI.ValidateBudget(); err != nil {
		t.Fatal(err)
	}
}
