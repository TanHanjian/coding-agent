package prompt

import (
	"context"
	"strings"
	"testing"

	promptcomponent "github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"
)

// Local prompt provider tests.
func TestLocalPromptProviderResolvesInterviewPrompt(t *testing.T) {
	provider := NewLocalPromptProvider()
	resolved, err := provider.Resolve(context.Background(), Request{Key: AgentPromptKey})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Key != AgentPromptKey || resolved.Version != EmbeddedVersion || resolved.Source != SourceLocal {
		t.Fatalf("resolved metadata = %#v", resolved)
	}
	if len(resolved.Templates) != 4 {
		t.Fatalf("template count = %d, want 4", len(resolved.Templates))
	}

	rendered, err := promptcomponent.FromMessages(schema.GoTemplate, resolved.Templates...).Format(context.Background(), map[string]any{
		"conversation_summary": "历史摘要",
		"interview_context":    "面试材料",
		"history":              []*schema.Message{schema.UserMessage("历史问题")},
		"query":                "当前问题",
	})
	if err != nil {
		t.Fatalf("Format() error = %v", err)
	}
	if len(rendered) != 4 {
		t.Fatalf("rendered message count = %d, want 4", len(rendered))
	}
	if rendered[0].Role != schema.System || !strings.Contains(rendered[0].Content, "面试复盘助手") {
		t.Fatalf("system message = %#v", rendered[0])
	}
	if rendered[1].Role != schema.System || !strings.Contains(rendered[1].Content, "历史摘要") || !strings.Contains(rendered[1].Content, "面试材料") {
		t.Fatalf("context message = %#v", rendered[1])
	}
	if rendered[2].Role != schema.User || rendered[2].Content != "历史问题" {
		t.Fatalf("history message = %#v", rendered[2])
	}
	if rendered[3].Role != schema.User || rendered[3].Content != "当前问题" {
		t.Fatalf("query message = %#v", rendered[3])
	}
}

func TestLocalPromptProviderReturnsIndependentTemplates(t *testing.T) {
	provider := NewLocalPromptProvider()
	first, err := provider.Resolve(context.Background(), Request{Key: AgentPromptKey})
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	firstMessage, ok := first.Templates[0].(*schema.Message)
	if !ok {
		t.Fatalf("first template type = %T, want *schema.Message", first.Templates[0])
	}
	firstMessage.Content = "mutated"

	second, err := provider.Resolve(context.Background(), Request{Key: AgentPromptKey})
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	secondMessage, ok := second.Templates[0].(*schema.Message)
	if !ok {
		t.Fatalf("second template type = %T, want *schema.Message", second.Templates[0])
	}
	if secondMessage.Content == "mutated" {
		t.Fatal("Resolve() reused mutable prompt templates")
	}
}

func TestLocalPromptProviderRejectsUnknownKey(t *testing.T) {
	_, err := NewLocalPromptProvider().Resolve(context.Background(), Request{Key: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported local prompt key") {
		t.Fatalf("Resolve() error = %v, want unknown key error", err)
	}
}

func TestLocalPromptProviderHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewLocalPromptProvider().Resolve(ctx, Request{Key: AgentPromptKey})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Resolve() error = %v, want context canceled", err)
	}
}

// Prompt hash tests.
func TestLocalPromptContentHashFingerprintsRenderedPromptWithoutExposingContent(t *testing.T) {
	provider := NewLocalPromptProvider()
	resolve := func(query string) ResolvedPrompt {
		resolved, err := provider.Resolve(context.Background(), Request{
			Key: AgentPromptKey,
			Variables: map[string]any{
				"history":              []*schema.Message{schema.UserMessage("history detail")},
				"query":                query,
				"interview_context":    "interview detail",
				"conversation_summary": "summary detail",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return resolved
	}
	first := resolve("private query alpha")
	second := resolve("private query beta")
	if first.ContentHash == "" || first.ContentHash == second.ContentHash {
		t.Fatalf("content hashes should be non-empty and input-sensitive: %q, %q", first.ContentHash, second.ContentHash)
	}
	if !strings.HasPrefix(first.ContentHash, "sha256:") || strings.Contains(first.ContentHash, "private query") || strings.Contains(first.ContentHash, "interview detail") {
		t.Fatalf("content hash contains raw prompt data: %q", first.ContentHash)
	}
}

func TestHashPromptTemplatesIsStable(t *testing.T) {
	left := HashPromptTemplates(interviewReviewTemplates())
	right := HashPromptTemplates(interviewReviewTemplates())
	if left == "" || left != right {
		t.Fatalf("template hashes = %q and %q", left, right)
	}
}
