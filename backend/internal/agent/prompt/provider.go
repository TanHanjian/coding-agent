// Package prompt defines the prompt seam used by the interview Agent.
package prompt

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

// Source identifies where a prompt definition came from.
type Source string

const (
	SourceLocal    Source = "local"
	SourceCozeLoop Source = "cozeloop"

	// AgentPromptKey is the stable key reserved for the interview review Agent
	// system prompt. Step 4 can resolve this key from CozeLoop Prompt Hub.
	AgentPromptKey = "interview-review-agent"

	// EmbeddedVersion identifies the prompt compiled into the application. It
	// is not a remotely published Prompt Hub version.
	EmbeddedVersion = "embedded"
)

// Request selects one prompt definition. Templates may still contain Eino
// runtime variables; Variables is reserved for providers that resolve or
// validate variables while loading a prompt.
type Request struct {
	Key       string
	Version   string
	Label     string
	Variables map[string]any
}

// ResolvedPrompt contains Eino-compatible templates. Keeping the Eino
// MessagesTemplate interface here is necessary because a prompt can contain
// both message templates and MessagesPlaceholder values for runtime history.
type ResolvedPrompt struct {
	Key            string
	Version        string
	Label          string
	Source         Source
	Fallback       bool
	FallbackReason string
	Templates      []schema.MessagesTemplate
}

// Provider is the small seam between an Agent builder and its prompt source.
// Implementations must return a complete, non-empty template sequence for the
// requested key and must not mutate a sequence returned by a previous call.
type Provider interface {
	Resolve(context.Context, Request) (ResolvedPrompt, error)
}

// ProviderFunc adapts a function to Provider, which keeps Builder tests small
// without requiring a concrete remote provider.
type ProviderFunc func(context.Context, Request) (ResolvedPrompt, error)

func (f ProviderFunc) Resolve(ctx context.Context, request Request) (ResolvedPrompt, error) {
	return f(ctx, request)
}
