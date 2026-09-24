// Package prompt defines the prompt seam used by the interview Agent.

package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"
)

// Prompt provider contract and selection metadata.
// Package prompt defines the prompt seam used by the interview Agent.

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
	ContentHash    string
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

// Rendered prompt content hashing.
// HashResolvedPrompt fingerprints the rendered Eino messages. Only the digest
// is returned; prompt content and runtime variables are never included in
// metadata or errors.
func HashResolvedPrompt(ctx context.Context, templates []schema.MessagesTemplate, variables map[string]any) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	messages := make([]*schema.Message, 0, len(templates))
	for index, template := range templates {
		if template == nil {
			return "", fmt.Errorf("prompt template %d is nil", index)
		}
		rendered, err := template.Format(ctx, variables, schema.GoTemplate)
		if err != nil {
			return "", fmt.Errorf("render prompt template %d: %w", index, err)
		}
		messages = append(messages, rendered...)
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return "", errors.New("cannot encode rendered prompt for hashing")
	}
	return hashPromptBytes(encoded), nil
}

// HashPromptTemplates is a metadata-only fallback for providers whose prompt
// cannot be rendered without runtime variables. It fingerprints template
// definitions, not user-provided values.
func HashPromptTemplates(templates []schema.MessagesTemplate) string {
	definitions := make([]string, 0, len(templates))
	for _, template := range templates {
		if template == nil {
			definitions = append(definitions, "<nil>")
			continue
		}
		if message, ok := template.(*schema.Message); ok {
			encoded, err := json.Marshal(message)
			if err == nil {
				definitions = append(definitions, string(encoded))
				continue
			}
		}
		definitions = append(definitions, fmt.Sprintf("%#v", template))
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return ""
	}
	return hashPromptBytes(encoded)
}

func hashPromptBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}
