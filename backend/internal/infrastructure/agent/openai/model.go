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
//
// 此函数由 cmd/server 的应用组装阶段调用一次。它不应由 HTTP Handler、
// Chat Service 或每次 AgentBuilder.Build 调用；模型客户端没有会话状态，
// 可以安全复用。
//
// 实现步骤：
//  1. 校验 APIKey 和 Model 均已配置，且不在错误或日志中输出 APIKey。
//  2. 将 APIKey、Model 和可选 BaseURL 映射到 Eino OpenAI 扩展的配置。
//  3. 调用扩展包的构造函数，并返回 model.BaseChatModel。
//  4. 由调用方将返回的模型注入用户实现的 AgentBuilder。
//
// 此函数只创建模型客户端，不接入 main。Agent 的提示词、工具、检索和运行策略
// 仍完全由 AgentBuilder 的实现决定。
func NewChatModel(ctx context.Context, cfg config.OpenAIConfig) (model.ToolCallingChatModel, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}
	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	})
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
	return nil
}
