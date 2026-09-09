package openai

import (
	"testing"

	"interview-memory-agent/backend/internal/infrastructure/config"
)

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(config.OpenAIConfig{}); err == nil {
		t.Fatal("expected API key validation error")
	}
	if err := ValidateConfig(config.OpenAIConfig{APIKey: "key"}); err == nil {
		t.Fatal("expected model validation error")
	}
	if err := ValidateConfig(config.OpenAIConfig{APIKey: "key", Model: "model"}); err != nil {
		t.Fatalf("expected valid OpenAI configuration: %v", err)
	}
}

func TestNewChatModelRejectsInvalidConfig(t *testing.T) {
	chatModel, err := NewChatModel(t.Context(), config.OpenAIConfig{})
	if err == nil {
		t.Fatalf("expected validation error, got model=%v", chatModel)
	}
}
