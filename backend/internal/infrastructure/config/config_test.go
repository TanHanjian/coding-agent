package config

import "testing"

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("APP_ADDR", "127.0.0.1:9090")
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("APP_LOG_LEVEL", "debug")
	t.Setenv("AGENT_DEBUG", "true")
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("OPENAI_MODEL", "test-model")
	t.Setenv("COZELOOP_ENABLED", "true")
	t.Setenv("COZELOOP_WORKSPACE_ID", "workspace-test")
	t.Setenv("COZELOOP_API_TOKEN", "token-test")
	t.Setenv("COZELOOP_ENVIRONMENT", " test ")
	t.Setenv("COZELOOP_SERVICE_NAME", " service-test ")
	t.Setenv("COZELOOP_CAPTURE_CONTENT", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9090" || cfg.LogLevel != "debug" || !cfg.AgentDebug {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.DatabasePath == "" {
		t.Fatal("expected database path")
	}
	if cfg.OpenAI.APIKey != "test-key" || cfg.OpenAI.BaseURL != "https://example.invalid/v1" || cfg.OpenAI.Model != "test-model" {
		t.Fatalf("unexpected OpenAI config: %+v", cfg.OpenAI)
	}
	if !cfg.CozeLoop.Enabled || cfg.CozeLoop.WorkspaceID != "workspace-test" || cfg.CozeLoop.APIToken != "token-test" {
		t.Fatalf("unexpected CozeLoop credentials config: %+v", cfg.CozeLoop)
	}
	if cfg.CozeLoop.Environment != "test" || cfg.CozeLoop.ServiceName != "service-test" || cfg.CozeLoop.CaptureContent {
		t.Fatalf("unexpected CozeLoop runtime config: %+v", cfg.CozeLoop)
	}
}

func TestLoadRejectsInvalidAgentDebug(t *testing.T) {
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("AGENT_DEBUG", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid AGENT_DEBUG error")
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

func TestLoadCozeLoopDefaults(t *testing.T) {
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("COZELOOP_ENABLED", "")
	t.Setenv("COZELOOP_WORKSPACE_ID", "")
	t.Setenv("COZELOOP_API_TOKEN", "")
	t.Setenv("COZELOOP_ENVIRONMENT", "")
	t.Setenv("COZELOOP_SERVICE_NAME", "")
	t.Setenv("COZELOOP_CAPTURE_CONTENT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CozeLoop.Enabled {
		t.Fatal("CozeLoop should be disabled by default")
	}
	if cfg.CozeLoop.Environment != "local" {
		t.Fatalf("Environment = %q, want local", cfg.CozeLoop.Environment)
	}
	if cfg.CozeLoop.ServiceName != "interview-memory-agent" {
		t.Fatalf("ServiceName = %q, want default service name", cfg.CozeLoop.ServiceName)
	}
	if !cfg.CozeLoop.CaptureContent {
		t.Fatal("CaptureContent should default to true")
	}
}

func TestLoadRejectsInvalidCozeLoopBoolean(t *testing.T) {
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("COZELOOP_ENABLED", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid COZELOOP_ENABLED error")
	}
}

func TestValidateForCozeLoop(t *testing.T) {
	if err := (Config{}).ValidateForCozeLoop(); err != nil {
		t.Fatalf("disabled CozeLoop should not require credentials: %v", err)
	}
	if err := (Config{CozeLoop: CozeLoopConfig{Enabled: true}}).ValidateForCozeLoop(); err == nil {
		t.Fatal("expected missing CozeLoop credentials error")
	}
	if err := (Config{CozeLoop: CozeLoopConfig{
		Enabled:     true,
		WorkspaceID: "workspace",
		APIToken:    "token",
	}}).ValidateForCozeLoop(); err != nil {
		t.Fatalf("expected valid CozeLoop configuration: %v", err)
	}
}
