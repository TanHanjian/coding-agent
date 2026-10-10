// Package openai 提供 OpenAI ChatModel 的基础设施组装入口。
package openai

import (
	"context"
	"errors"
	"strings"

	"interview-memory-agent/backend/internal/infrastructure/config"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewChatModel 根据进程本地 OpenAI 配置创建可复用的 Eino ChatModel。
// 客户端没有会话状态，可由应用组装根创建后供 AgentBuilder 复用。
//
// 提供任何预算字段时必须通过完整预算校验，并将 MaxOutputTokens 映射到
// OutputLimitMode 指定的唯一输出上限字段。预算完全未配置时保留独立
// eval/judge 的无 cap 行为；服务器聊天必须先调用 Config.ValidateForChat。
// 当前装配只支持此配置快照，不支持通过单次调用 option 或 ExtraFields
// 覆盖模型身份、输出上限或模式；调用方不得使用这些 override 绕过预算。
func NewChatModel(ctx context.Context, cfg config.OpenAIConfig) (model.ToolCallingChatModel, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	clientConfig := &einoopenai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	}
	switch cfg.OutputLimitMode {
	case "max_tokens":
		clientConfig.MaxTokens = &cfg.MaxOutputTokens
	case "max_completion_tokens":
		clientConfig.MaxCompletionTokens = &cfg.MaxOutputTokens
	}
	chatModel, err := einoopenai.NewChatModel(ctx, clientConfig)
	if err != nil {
		return nil, err
	}
	return chatModel, nil
}

// ValidateConfig 提供给 NewChatModel 及未来启动组装逻辑复用，避免在模型
// 创建前才因缺少配置发起无效请求。
func ValidateConfig(cfg config.OpenAIConfig) error {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return errors.New("OPENAI_API_KEY is required when chat is enabled")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return errors.New("OPENAI_MODEL is required when chat is enabled")
	}
	if cfg.HasBudget() {
		return cfg.ValidateBudget()
	}
	return nil
}
