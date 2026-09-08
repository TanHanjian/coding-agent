package config

import "testing"

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("APP_ADDR", "127.0.0.1:9090")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("APP_LOG_LEVEL", "debug")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("OPENAI_MODEL", "test-model")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" || cfg.LogLevel != "debug" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DatabasePath == "" {
		t.Fatal("expected database path")
	}
	if cfg.OpenAI.APIKey != "test-key" || cfg.OpenAI.BaseURL != "https://example.invalid/v1" || cfg.OpenAI.Model != "test-model" {
		t.Fatalf("unexpected OpenAI config: %+v", cfg.OpenAI)
	}
}

func TestValidateForChat(t *testing.T) {
	if err := (Config{}).ValidateForChat(); err == nil {
		t.Fatal("expected API key validation error")
	}
	if err := (Config{OpenAI: OpenAIConfig{APIKey: "key"}}).ValidateForChat(); err == nil {
		t.Fatal("expected model validation error")
	}
	if err := (Config{OpenAI: OpenAIConfig{APIKey: "key", Model: "model"}}).ValidateForChat(); err != nil {
		t.Fatalf("expected valid chat configuration: %v", err)
	}
}
