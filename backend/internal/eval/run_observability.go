package eval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
)

// Run identity and execution metadata.
// RunMetadata describes a single candidate execution. A CLI invocation shares
// one RunID; CaseID, CaseVersion, and RepeatIndex distinguish every execution
// within it and make a report upload idempotent.
type RunMetadata struct {
	RunID          string
	CaseID         string
	CaseVersion    int
	CaseTags       []string
	RepeatIndex    int
	GitCommit      string
	CandidateModel string
	JudgeModel     string
	PromptKey      string
	PromptVersion  string
	PromptLabel    string
	PromptSource   string
}

func NewRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate evaluation run id: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40 // UUID version 4
	value[8] = (value[8] & 0x3f) | 0x80 // RFC 4122 variant
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

// Trace capture and usage state.
// TraceMetadata is the safe, non-content identity attached to one Candidate
// or Judge evaluation trace. It is also the correlation key used by the
// CozeLoop adapter to return a platform trace ID to the local report.
type TraceMetadata struct {
	RunID         string
	CaseID        string
	CaseVersion   int
	CaseTags      []string
	RepeatIndex   int
	Role          string
	GitCommit     string
	Model         string
	PromptKey     string
	PromptVersion string
	PromptLabel   string
	PromptSource  string
}

// TraceCapture aggregates the trace reference and actual usage for one role
// within one evaluation execution. It is safe for concurrent Eino callbacks.
type TraceCapture struct {
	metadata TraceMetadata

	mu               sync.Mutex
	traceID          string
	usage            *TokenUsage
	promptResolution *PromptResolution
}

func NewTraceCapture(metadata TraceMetadata) *TraceCapture {
	metadata.CaseTags = append([]string(nil), metadata.CaseTags...)
	return &TraceCapture{metadata: metadata}
}

func WithTraceCapture(ctx context.Context, capture *TraceCapture) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, traceCaptureContextKey{}, capture)
}

func TraceCaptureFromContext(ctx context.Context) *TraceCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(traceCaptureContextKey{}).(*TraceCapture)
	return capture
}

func (c *TraceCapture) Metadata() TraceMetadata {
	if c == nil {
		return TraceMetadata{}
	}
	metadata := c.metadata
	metadata.CaseTags = append([]string(nil), c.metadata.CaseTags...)
	return metadata
}

func (c *TraceCapture) SetTraceID(traceID string) {
	if c == nil {
		return
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.traceID == "" {
		c.traceID = traceID
	}
}

func (c *TraceCapture) SetPromptResolution(resolution PromptResolution) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptResolution == nil {
		copyResolution := resolution
		c.promptResolution = &copyResolution
	}
}

func (c *TraceCapture) PromptResolution() *PromptResolution {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.promptResolution == nil {
		return nil
	}
	copyResolution := *c.promptResolution
	return &copyResolution
}

func (c *TraceCapture) AddUsage(usage *schema.TokenUsage) {
	if c == nil || usage == nil {
		return
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CompletionTokens
	}
	c.mu.Lock()
	if c.usage == nil {
		c.usage = &TokenUsage{}
	}
	c.usage.PromptTokens += usage.PromptTokens
	c.usage.CompletionTokens += usage.CompletionTokens
	c.usage.TotalTokens += total
	c.mu.Unlock()
}

func (c *TraceCapture) Snapshot() TraceExecution {
	if c == nil {
		return TraceExecution{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var usage *TokenUsage
	if c.usage != nil {
		copyUsage := *c.usage
		usage = &copyUsage
	}
	return TraceExecution{TraceID: c.traceID, Usage: usage}
}

type traceCaptureContextKey struct{}

// Resolved prompt audit metadata.
type promptAuditProvider struct {
	delegate agentprompt.Provider
}

func (p promptAuditProvider) Resolve(ctx context.Context, request agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
	resolved, err := p.delegate.Resolve(ctx, request)
	if err != nil {
		return agentprompt.ResolvedPrompt{}, err
	}
	if resolved.ContentHash == "" {
		if hash, hashErr := agentprompt.HashResolvedPrompt(ctx, resolved.Templates, request.Variables); hashErr == nil {
			resolved.ContentHash = hash
		} else {
			resolved.ContentHash = agentprompt.HashPromptTemplates(resolved.Templates)
		}
	}
	capture := TraceCaptureFromContext(ctx)
	if capture != nil {
		metadata := capture.Metadata()
		requested := PromptSelection{
			Key:     firstNonEmpty(metadata.PromptKey, request.Key),
			Version: firstNonEmpty(metadata.PromptVersion, request.Version),
			Label:   firstNonEmpty(metadata.PromptLabel, request.Label),
			Source:  metadata.PromptSource,
		}
		resolvedSelection := PromptSelection{
			Key:     resolved.Key,
			Version: resolved.Version,
			Label:   resolved.Label,
			Source:  string(resolved.Source),
		}
		capture.SetPromptResolution(PromptResolution{
			Requested:      requested,
			Resolved:       resolvedSelection,
			Fallback:       resolved.Fallback,
			FallbackReason: resolved.FallbackReason,
			ContentHash:    resolved.ContentHash,
		})
	}
	return resolved, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// Eino model usage callback.
func newCandidateUsageCallback() callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
			if !isChatModelCallback(info) {
				return ctx
			}
			capture := TraceCaptureFromContext(ctx)
			if capture == nil {
				return ctx
			}
			if callbackOutput := model.ConvCallbackOutput(output); callbackOutput != nil {
				capture.AddUsage(callbackOutputUsage(callbackOutput))
			}
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if output == nil {
				return ctx
			}
			defer output.Close()
			if !isChatModelCallback(info) {
				return ctx
			}
			capture := TraceCaptureFromContext(ctx)
			if capture == nil {
				return ctx
			}
			for {
				chunk, err := output.Recv()
				if errors.Is(err, io.EOF) || err != nil {
					return ctx
				}
				if callbackOutput := model.ConvCallbackOutput(chunk); callbackOutput != nil {
					capture.AddUsage(callbackOutputUsage(callbackOutput))
				}
			}
		}).
		Build()
}

func isChatModelCallback(info *callbacks.RunInfo) bool {
	return info != nil && info.Component == components.ComponentOfChatModel
}

func callbackOutputUsage(output *model.CallbackOutput) *schema.TokenUsage {
	if output == nil {
		return nil
	}
	if output.Message != nil && output.Message.ResponseMeta != nil && output.Message.ResponseMeta.Usage != nil {
		return output.Message.ResponseMeta.Usage
	}
	if output.TokenUsage == nil {
		return nil
	}
	return &schema.TokenUsage{
		PromptTokens:     output.TokenUsage.PromptTokens,
		CompletionTokens: output.TokenUsage.CompletionTokens,
		TotalTokens:      output.TokenUsage.TotalTokens,
	}
}
