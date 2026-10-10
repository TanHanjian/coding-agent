package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// HasBudget 判断是否提供了任何预算配置。通用模型工厂可在完全未配置时
// 保持独立 eval/judge 的兼容行为；聊天运行时必须无条件调用 ValidateBudget。
func (c OpenAIConfig) HasBudget() bool {
	return c.ContextWindowTokens != 0 || c.MaxOutputTokens != 0 ||
		c.SafetyMarginTokens != 0 || c.SafetyMarginConfigured || c.OutputLimitMode != ""
}

// ValidateBudget 校验完整、显式的模型预算。它不推断模型容量或默认余量，
// 错误只包含配置字段名和约束，不包含配置值。
func (c OpenAIConfig) ValidateBudget() error {
	if c.ContextWindowTokens <= 0 {
		return errors.New("OPENAI_CONTEXT_WINDOW_TOKENS must be explicitly configured and greater than zero")
	}
	if c.MaxOutputTokens <= 0 {
		return errors.New("OPENAI_MAX_OUTPUT_TOKENS must be explicitly configured and greater than zero")
	}
	if !c.SafetyMarginConfigured {
		return errors.New("OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS must be explicitly configured (zero is allowed)")
	}
	if c.SafetyMarginTokens < 0 {
		return errors.New("OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS must be non-negative")
	}
	// 先比较输出，再用减法比较余量，避免 output + margin 溢出。
	if c.MaxOutputTokens >= c.ContextWindowTokens ||
		c.SafetyMarginTokens >= c.ContextWindowTokens-c.MaxOutputTokens {
		return errors.New("OPENAI_MAX_OUTPUT_TOKENS plus OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS must be less than OPENAI_CONTEXT_WINDOW_TOKENS")
	}
	return validateOutputLimitMode(c.OutputLimitMode)
}

func validateOutputLimitMode(mode string) error {
	if mode != "max_tokens" && mode != "max_completion_tokens" {
		return errors.New("OPENAI_OUTPUT_LIMIT_MODE must be explicitly configured as max_tokens or max_completion_tokens")
	}
	return nil
}

func loadOpenAIConfig() (OpenAIConfig, error) {
	cfg := OpenAIConfig{
		APIKey:          os.Getenv("OPENAI_API_KEY"),
		BaseURL:         os.Getenv("OPENAI_BASE_URL"),
		Model:           os.Getenv("OPENAI_MODEL"),
		OutputLimitMode: strings.TrimSpace(os.Getenv("OPENAI_OUTPUT_LIMIT_MODE")),
	}
	var err error
	cfg.ContextWindowTokens, _, err = envBudgetTokens("OPENAI_CONTEXT_WINDOW_TOKENS", 1)
	if err != nil {
		return OpenAIConfig{}, err
	}
	cfg.MaxOutputTokens, _, err = envBudgetTokens("OPENAI_MAX_OUTPUT_TOKENS", 1)
	if err != nil {
		return OpenAIConfig{}, err
	}
	cfg.SafetyMarginTokens, cfg.SafetyMarginConfigured, err = envBudgetTokens("OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS", 0)
	if err != nil {
		return OpenAIConfig{}, err
	}
	if cfg.OutputLimitMode != "" {
		if err := validateOutputLimitMode(cfg.OutputLimitMode); err != nil {
			return OpenAIConfig{}, err
		}
	}
	return cfg, nil
}

func envBudgetTokens(key string, minimum int) (int, bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		// 不包装 strconv 的错误，其错误正文包含原始配置值。
		return 0, true, fmt.Errorf("%s must be an integer within the supported range", key)
	}
	if parsed < minimum {
		return 0, true, fmt.Errorf("%s must be an integer greater than or equal to %d", key, minimum)
	}
	return parsed, true, nil
}
