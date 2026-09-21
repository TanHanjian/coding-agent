package cozeloop

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	"interview-memory-agent/backend/internal/infrastructure/config"

	"github.com/cloudwego/eino/schema"
	cozeloopsdk "github.com/coze-dev/cozeloop-go"
	"github.com/coze-dev/cozeloop-go/entity"
)

type fakePromptClient struct {
	prompt          *entity.Prompt
	getErr          error
	formatErr       error
	getParam        cozeloopsdk.GetPromptParam
	formatVariables map[string]any
}

func (f *fakePromptClient) getPrompt(_ context.Context, param cozeloopsdk.GetPromptParam) (*entity.Prompt, error) {
	f.getParam = param
	return f.prompt, f.getErr
}

func (f *fakePromptClient) formatPrompt(_ context.Context, _ *entity.Prompt, variables map[string]any) ([]*entity.Message, error) {
	f.formatVariables = variables
	if f.formatErr != nil {
		return nil, f.formatErr
	}
	return []*entity.Message{
		{Role: entity.RoleSystem, Content: stringPointer("remote system")},
		{Role: entity.RoleUser, Content: stringPointer("remote question")},
	}, nil
}

func TestCozeLoopPromptProviderFormatsAndConvertsPrompt(t *testing.T) {
	client := &fakePromptClient{
		prompt: &entity.Prompt{PromptKey: "remote-interview", Version: "7"},
	}
	provider := newPromptProvider(client, config.CozeLoopConfig{
		Enabled:       true,
		PromptEnabled: true,
		PromptKey:     "remote-interview",
		PromptLabel:   "production",
	}, agentprompt.NewLocalPromptProvider())

	resolved, err := provider.Resolve(context.Background(), agentprompt.Request{
		Key: agentprompt.AgentPromptKey,
		Variables: map[string]any{
			"history": []*schema.Message{
				schema.UserMessage("previous question"),
				schema.AssistantMessage("previous answer", nil),
			},
			"query": "current question",
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if client.getParam.PromptKey != "remote-interview" || client.getParam.Label != "production" || client.getParam.Version != "" {
		t.Fatalf("GetPrompt params = %+v", client.getParam)
	}
	history, ok := client.formatVariables["history"].([]*entity.Message)
	if !ok || len(history) != 2 || history[0].Role != entity.RoleUser || history[1].Role != entity.RoleAssistant {
		t.Fatalf("formatted history = %#v", client.formatVariables["history"])
	}
	if resolved.Source != agentprompt.SourceCozeLoop || resolved.Key != "remote-interview" || resolved.Version != "7" {
		t.Fatalf("resolved metadata = %+v", resolved)
	}
	if len(resolved.Templates) != 2 {
		t.Fatalf("resolved templates = %d, want 2", len(resolved.Templates))
	}
	if message, ok := resolved.Templates[0].(*schema.Message); !ok || message.Role != schema.System || message.Content != "remote system" {
		t.Fatalf("first resolved template = %#v", resolved.Templates[0])
	}
}

func TestCozeLoopPromptProviderUsesVersionInsteadOfLabel(t *testing.T) {
	client := &fakePromptClient{
		prompt: &entity.Prompt{PromptKey: "remote-interview", Version: "7"},
	}
	provider := newPromptProvider(client, config.CozeLoopConfig{
		PromptKey:     "remote-interview",
		PromptVersion: "6",
		PromptLabel:   "production",
	}, agentprompt.NewLocalPromptProvider())

	if _, err := provider.Resolve(context.Background(), agentprompt.Request{Key: agentprompt.AgentPromptKey}); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if client.getParam.Version != "6" || client.getParam.Label != "" {
		t.Fatalf("GetPrompt params = %+v, want fixed version without label", client.getParam)
	}
}

func TestCozeLoopPromptProviderPropagatesCancellation(t *testing.T) {
	client := &fakePromptClient{getErr: context.Canceled}
	provider := newPromptProvider(client, config.CozeLoopConfig{
		PromptKey: "remote-interview",
	}, agentprompt.NewLocalPromptProvider())

	_, err := provider.Resolve(context.Background(), agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}
}

func TestCozeLoopPromptProviderRedactsTokenWhenFallbackFails(t *testing.T) {
	const token = "prompt-secret-token"
	client := &fakePromptClient{getErr: errors.New("remote request failed with prompt-secret-token")}
	fallback := agentprompt.ProviderFunc(func(context.Context, agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
		return agentprompt.ResolvedPrompt{}, errors.New("local prompt unavailable")
	})
	provider := newPromptProvider(client, config.CozeLoopConfig{
		APIToken:  token,
		PromptKey: agentprompt.AgentPromptKey,
	}, fallback)

	_, err := provider.Resolve(context.Background(), agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("Resolve() error = %v, want redacted token", err)
	}
}

func TestCozeLoopPromptProviderPropagatesCancellationDuringFallback(t *testing.T) {
	client := &fakePromptClient{getErr: errors.New("remote unavailable")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fallback := agentprompt.ProviderFunc(func(context.Context, agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
		cancel()
		return agentprompt.ResolvedPrompt{Key: agentprompt.AgentPromptKey, Source: agentprompt.SourceLocal}, nil
	})
	provider := newPromptProvider(client, config.CozeLoopConfig{
		PromptKey: "remote-interview",
	}, fallback)

	_, err := provider.Resolve(ctx, agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve() error = %v, want context.Canceled", err)
	}
}

func TestCozeLoopPromptProviderStrictModeDoesNotFallBackToLocalPrompt(t *testing.T) {
	client := &fakePromptClient{getErr: errors.New("remote unavailable")}
	provider := newPromptProviderWithFallback(client, config.CozeLoopConfig{
		PromptKey: agentprompt.AgentPromptKey,
	}, agentprompt.NewLocalPromptProvider(), false)

	resolved, err := provider.Resolve(context.Background(), agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if err == nil {
		t.Fatal("Resolve() error = nil, want strict remote resolution error")
	}
	if resolved.Templates != nil {
		t.Fatalf("strict resolution returned templates = %#v", resolved.Templates)
	}
	if !strings.Contains(err.Error(), "get_prompt") || strings.Contains(err.Error(), "remote unavailable") {
		t.Fatalf("strict resolution error = %v", err)
	}
}

func TestCozeLoopPromptProviderFallsBackToLocalPrompt(t *testing.T) {
	client := &fakePromptClient{getErr: errors.New("remote unavailable")}
	provider := newPromptProvider(client, config.CozeLoopConfig{
		PromptKey: "remote-interview",
	}, agentprompt.NewLocalPromptProvider())

	resolved, err := provider.Resolve(context.Background(), agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Source != agentprompt.SourceLocal || resolved.Version != agentprompt.EmbeddedVersion || !resolved.Fallback || resolved.FallbackReason != "get_prompt" {
		t.Fatalf("fallback metadata = %+v", resolved)
	}
	if len(resolved.Templates) == 0 {
		t.Fatal("fallback returned no templates")
	}
}

func TestConvertCozeMessagesRejectsUnsupportedContent(t *testing.T) {
	_, err := convertCozeMessages([]*entity.Message{{
		Role:  entity.RoleSystem,
		Parts: []*entity.ContentPart{{Type: entity.ContentTypeImageURL}},
	}})
	if err == nil {
		t.Fatal("convertCozeMessages() error = nil, want multipart validation error")
	}
}

func stringPointer(value string) *string { return &value }
