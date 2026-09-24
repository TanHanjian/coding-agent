package cozeloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	"interview-memory-agent/backend/internal/infrastructure/config"

	"github.com/cloudwego/eino/schema"
	cozeloopsdk "github.com/coze-dev/cozeloop-go"
	"github.com/coze-dev/cozeloop-go/entity"
)

// promptClient is the small SDK surface needed by PromptProvider. Keeping this
// interface separate makes remote prompt behavior testable without a network
// request or real CozeLoop credentials.
type promptClient interface {
	getPrompt(context.Context, cozeloopsdk.GetPromptParam) (*entity.Prompt, error)
	formatPrompt(context.Context, *entity.Prompt, map[string]any) ([]*entity.Message, error)
}

type sdkPromptClient struct {
	client cozeloopsdk.Client
}

func (c sdkPromptClient) getPrompt(ctx context.Context, param cozeloopsdk.GetPromptParam) (*entity.Prompt, error) {
	return c.client.GetPrompt(ctx, param)
}

func (c sdkPromptClient) formatPrompt(ctx context.Context, prompt *entity.Prompt, variables map[string]any) ([]*entity.Message, error) {
	return c.client.PromptFormat(ctx, prompt, variables)
}

// CozeLoopPromptProvider resolves an Agent prompt from CozeLoop Prompt Hub
// and falls back to a local provider when the remote prompt cannot be used.
// The SDK itself owns the remote prompt cache; this adapter owns selection and
// conversion into the Eino prompt seam.
type CozeLoopPromptProvider struct {
	client        promptClient
	config        config.CozeLoopConfig
	fallback      agentprompt.Provider
	allowFallback bool
}

// NewPromptProvider creates the production CozeLoop Prompt Hub adapter.
func NewPromptProvider(client *Client, fallback agentprompt.Provider) (*CozeLoopPromptProvider, error) {
	if client == nil || !client.Enabled() || client.sdkClient == nil {
		return nil, errors.New("CozeLoop Prompt Provider requires an enabled client")
	}
	if fallback == nil {
		fallback = agentprompt.NewLocalPromptProvider()
	}
	return newPromptProvider(sdkPromptClient{client: client.sdkClient}, client.cfg, fallback), nil
}

// NewStrictPromptProvider creates a Prompt Hub adapter that never silently
// falls back to the local prompt. It is intended for fixed-version release
// evaluations where a local result would invalidate the evaluation evidence.
func NewStrictPromptProvider(client *Client, fallback agentprompt.Provider) (*CozeLoopPromptProvider, error) {
	if client == nil || !client.Enabled() || client.sdkClient == nil {
		return nil, errors.New("CozeLoop Prompt Provider requires an enabled client")
	}
	if fallback == nil {
		fallback = agentprompt.NewLocalPromptProvider()
	}
	return newPromptProviderWithFallback(sdkPromptClient{client: client.sdkClient}, client.cfg, fallback, false), nil
}

func newPromptProvider(client promptClient, cfg config.CozeLoopConfig, fallback agentprompt.Provider) *CozeLoopPromptProvider {
	return newPromptProviderWithFallback(client, cfg, fallback, true)
}

func newPromptProviderWithFallback(client promptClient, cfg config.CozeLoopConfig, fallback agentprompt.Provider, allowFallback bool) *CozeLoopPromptProvider {
	if fallback == nil {
		fallback = agentprompt.NewLocalPromptProvider()
	}
	return &CozeLoopPromptProvider{
		client:        client,
		config:        cfg,
		fallback:      fallback,
		allowFallback: allowFallback,
	}
}

func (p *CozeLoopPromptProvider) Resolve(ctx context.Context, request agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}

	request, err := p.withDefaults(request)
	if err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}

	remotePrompt, err := p.client.getPrompt(ctx, cozeloopsdk.GetPromptParam{
		PromptKey: request.Key,
		Version:   request.Version,
		Label:     request.Label,
	})
	if err != nil {
		if isContextError(err) {
			return agentprompt.ResolvedPrompt{}, err
		}
		return p.resolveFallback(ctx, request, "get_prompt", fmt.Errorf("get remote prompt: %w", err))
	}
	if remotePrompt == nil {
		return p.resolveFallback(ctx, request, "not_found", errors.New("get remote prompt: prompt was not found"))
	}
	if err := ctx.Err(); err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}

	variables, err := cozePromptVariables(request.Variables)
	if err != nil {
		return p.resolveFallback(ctx, request, "variables", fmt.Errorf("prepare prompt variables: %w", err))
	}
	messages, err := p.client.formatPrompt(ctx, remotePrompt, variables)
	if err != nil {
		if isContextError(err) {
			return agentprompt.ResolvedPrompt{}, err
		}
		return p.resolveFallback(ctx, request, "format_prompt", fmt.Errorf("format remote prompt: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}
	templates, err := convertCozeMessages(messages)
	if err != nil {
		return p.resolveFallback(ctx, request, "convert_prompt", fmt.Errorf("convert remote prompt: %w", err))
	}
	if len(templates) == 0 {
		return p.resolveFallback(ctx, request, "empty_prompt", errors.New("format remote prompt: no messages returned"))
	}

	key := strings.TrimSpace(remotePrompt.PromptKey)
	if key == "" {
		key = request.Key
	}
	version := strings.TrimSpace(remotePrompt.Version)
	if version == "" {
		version = request.Version
	}
	contentHash, err := agentprompt.HashResolvedPrompt(ctx, templates, request.Variables)
	if err != nil {
		contentHash = agentprompt.HashPromptTemplates(templates)
	}
	return agentprompt.ResolvedPrompt{
		Key:         key,
		Version:     version,
		Label:       request.Label,
		Source:      agentprompt.SourceCozeLoop,
		ContentHash: contentHash,
		Templates:   templates,
	}, nil
}

func (p *CozeLoopPromptProvider) withDefaults(request agentprompt.Request) (agentprompt.Request, error) {
	request.Key = strings.TrimSpace(request.Key)
	configuredKey := strings.TrimSpace(p.config.PromptKey)
	if request.Key == "" || (request.Key == agentprompt.AgentPromptKey && configuredKey != "") {
		request.Key = configuredKey
	}
	if request.Key == "" {
		return agentprompt.Request{}, errors.New("CozeLoop prompt provider: prompt key is required")
	}

	request.Version = strings.TrimSpace(request.Version)
	if request.Version == "" {
		request.Version = strings.TrimSpace(p.config.PromptVersion)
	}
	request.Label = strings.TrimSpace(request.Label)
	if request.Label == "" {
		request.Label = strings.TrimSpace(p.config.PromptLabel)
	}
	// CozeLoop selects a concrete version before a label when both are sent.
	// Clear the label so callers cannot accidentally request an ambiguous pair.
	if request.Version != "" {
		request.Label = ""
	}
	return request, nil
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (p *CozeLoopPromptProvider) resolveFallback(
	ctx context.Context,
	request agentprompt.Request,
	reason string,
	remoteErr error,
) (agentprompt.ResolvedPrompt, error) {
	if !p.allowFallback {
		if err := ctx.Err(); err != nil {
			return agentprompt.ResolvedPrompt{}, err
		}
		return agentprompt.ResolvedPrompt{}, fmt.Errorf("CozeLoop prompt resolution failed: %s", reason)
	}

	fallbackRequest := request
	fallbackRequest.Key = agentprompt.AgentPromptKey
	fallbackRequest.Version = ""
	fallbackRequest.Label = ""
	if err := ctx.Err(); err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}
	resolved, err := p.fallback.Resolve(ctx, fallbackRequest)
	if err != nil {
		remoteMessage := redact(remoteErr.Error(), p.config.APIToken)
		return agentprompt.ResolvedPrompt{}, fmt.Errorf("%s; local fallback: %w", remoteMessage, err)
	}
	if err := ctx.Err(); err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}
	resolved.Fallback = true
	resolved.FallbackReason = reason
	slog.WarnContext(ctx, "CozeLoop Prompt Hub unavailable; using local prompt", "prompt_key", request.Key, "reason", reason)
	return resolved, nil
}

func cozePromptVariables(variables map[string]any) (map[string]any, error) {
	result := make(map[string]any, len(variables))
	for key, value := range variables {
		result[key] = value
	}

	history, ok := result["history"]
	if !ok {
		return result, nil
	}
	schemaMessages, ok := history.([]*schema.Message)
	if !ok {
		return nil, fmt.Errorf("history must be []*schema.Message, got %T", history)
	}
	converted, err := schemaMessagesToCoze(schemaMessages)
	if err != nil {
		return nil, err
	}
	result["history"] = converted
	return result, nil
}

func schemaMessagesToCoze(messages []*schema.Message) ([]*entity.Message, error) {
	result := make([]*entity.Message, 0, len(messages))
	for i, message := range messages {
		if message == nil {
			continue
		}
		role, err := schemaRoleToCoze(message.Role)
		if err != nil {
			return nil, fmt.Errorf("history message %d: %w", i, err)
		}
		content := message.Content
		converted := &entity.Message{
			Role:    role,
			Content: &content,
		}
		if message.ToolCallID != "" {
			toolCallID := message.ToolCallID
			converted.ToolCallID = &toolCallID
		}
		result = append(result, converted)
	}
	return result, nil
}

func schemaRoleToCoze(role schema.RoleType) (entity.Role, error) {
	switch role {
	case schema.System:
		return entity.RoleSystem, nil
	case schema.User:
		return entity.RoleUser, nil
	case schema.Assistant:
		return entity.RoleAssistant, nil
	case schema.Tool:
		return entity.RoleTool, nil
	default:
		return "", fmt.Errorf("unsupported Eino message role %q", role)
	}
}

func convertCozeMessages(messages []*entity.Message) ([]schema.MessagesTemplate, error) {
	result := make([]schema.MessagesTemplate, 0, len(messages))
	for i, message := range messages {
		if message == nil {
			continue
		}
		if message.Role == entity.RolePlaceholder {
			return nil, fmt.Errorf("message %d contains an unresolved placeholder", i)
		}
		if len(message.Parts) > 0 {
			return nil, fmt.Errorf("message %d contains unsupported multipart content", i)
		}
		if len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("message %d contains unsupported tool calls", i)
		}
		role, err := cozeRoleToSchema(message.Role)
		if err != nil {
			return nil, fmt.Errorf("message %d: %w", i, err)
		}
		content := ""
		if message.Content != nil {
			content = *message.Content
		}
		converted := &schema.Message{
			Role:    role,
			Content: content,
		}
		if message.ToolCallID != nil {
			converted.ToolCallID = *message.ToolCallID
		}
		result = append(result, converted)
	}
	return result, nil
}

func cozeRoleToSchema(role entity.Role) (schema.RoleType, error) {
	switch role {
	case entity.RoleSystem:
		return schema.System, nil
	case entity.RoleUser:
		return schema.User, nil
	case entity.RoleAssistant:
		return schema.Assistant, nil
	case entity.RoleTool:
		return schema.Tool, nil
	default:
		return "", fmt.Errorf("unsupported CozeLoop message role %q", role)
	}
}

var _ agentprompt.Provider = (*CozeLoopPromptProvider)(nil)
