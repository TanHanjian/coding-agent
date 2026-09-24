package eval

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
)

// Run identity tests.
func TestNewRunIDReturnsUniqueUUID(t *testing.T) {
	first, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("NewRunID() returned duplicate id %q", first)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first) {
		t.Fatalf("NewRunID() = %q, want UUID v4", first)
	}
}

func TestRunCaseWithMetadataRejectsMismatchedIdentity(t *testing.T) {
	caseData := EvalCase{ID: "case-a", Version: DatasetVersion, Input: EvalInput{Query: "question"}}
	result := (&Runner{}).RunCaseWithMetadata(context.Background(), caseData, time.Second, RunMetadata{
		RunID:       "run-1",
		CaseID:      "case-b",
		CaseVersion: DatasetVersion,
		RepeatIndex: 1,
	})
	if result.Status != "failed" || result.Error != "invalid_run_identity" {
		t.Fatalf("result = %+v, want invalid identity failure", result)
	}
}

// Trace capture tests.
func TestTraceCaptureAggregatesUsageAndKeepsFirstTraceID(t *testing.T) {
	capture := NewTraceCapture(TraceMetadata{RunID: "run-1", CaseTags: []string{"smoke"}})
	capture.SetTraceID("trace-1")
	capture.SetTraceID("trace-2")
	capture.AddUsage(&schema.TokenUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8})
	capture.AddUsage(&schema.TokenUsage{PromptTokens: 7, CompletionTokens: 2})

	snapshot := capture.Snapshot()
	if snapshot.TraceID != "trace-1" {
		t.Fatalf("TraceID = %q, want first trace id", snapshot.TraceID)
	}
	if snapshot.Usage == nil || *snapshot.Usage != (TokenUsage{PromptTokens: 10, CompletionTokens: 7, TotalTokens: 17}) {
		t.Fatalf("Usage = %+v", snapshot.Usage)
	}

	metadata := capture.Metadata()
	metadata.CaseTags[0] = "mutated"
	if capture.Metadata().CaseTags[0] != "smoke" {
		t.Fatal("Metadata() exposed the internal case tag slice")
	}
}

func TestTraceCaptureContextLookup(t *testing.T) {
	capture := NewTraceCapture(TraceMetadata{RunID: "run-1"})
	ctx := WithTraceCapture(context.Background(), capture)
	if got := TraceCaptureFromContext(ctx); got != capture {
		t.Fatalf("TraceCaptureFromContext() = %p, want %p", got, capture)
	}
	if got := TraceCaptureFromContext(context.Background()); got != nil {
		t.Fatalf("TraceCaptureFromContext(background) = %p, want nil", got)
	}
}

// Prompt audit tests.
func TestPromptAuditProviderCapturesRequestedAndResolvedPromptMetadata(t *testing.T) {
	provider := promptAuditProvider{delegate: agentprompt.ProviderFunc(func(context.Context, agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
		return agentprompt.ResolvedPrompt{
			Key:            "remote-key",
			Version:        "12",
			Source:         agentprompt.SourceCozeLoop,
			Fallback:       true,
			FallbackReason: "get_prompt",
			ContentHash:    "sha256:abc123",
			Templates:      []schema.MessagesTemplate{schema.SystemMessage("private system prompt")},
		}, nil
	})}
	capture := NewTraceCapture(TraceMetadata{
		PromptKey:     "requested-key",
		PromptVersion: "12",
		PromptSource:  "cozeloop",
	})
	ctx := WithTraceCapture(context.Background(), capture)
	resolved, err := provider.Resolve(ctx, agentprompt.Request{Key: agentprompt.AgentPromptKey})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ContentHash != "sha256:abc123" {
		t.Fatalf("resolved content hash = %q", resolved.ContentHash)
	}
	prompt := capture.PromptResolution()
	if prompt == nil || prompt.Requested.Key != "requested-key" || prompt.Requested.Version != "12" || prompt.Resolved.Key != "remote-key" || prompt.Resolved.Source != "cozeloop" || !prompt.Fallback || prompt.FallbackReason != "get_prompt" || prompt.ContentHash != "sha256:abc123" {
		t.Fatalf("prompt resolution = %+v", prompt)
	}
}
