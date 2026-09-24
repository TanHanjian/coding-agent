package cozeloop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cozeloopcallback "github.com/cloudwego/eino-ext/callbacks/cozeloop"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

func TestRegisterEinoTracingOnlyOnce(t *testing.T) {
	originalAppend := appendGlobalHandlers
	appendCalls := 0
	appendGlobalHandlers = func(handlers ...callbacks.Handler) {
		appendCalls += len(handlers)
	}
	defer func() { appendGlobalHandlers = originalAppend }()

	client, err := New(config.CozeLoopConfig{
		Enabled:        true,
		WorkspaceID:    "step2-test-workspace-" + strings.ReplaceAll(t.Name(), "/", "-"),
		APIToken:       "step2-test-token",
		CaptureContent: false,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close(context.Background())
	secondClient, err := New(config.CozeLoopConfig{
		Enabled:        true,
		WorkspaceID:    "step2-test-workspace-second-" + strings.ReplaceAll(t.Name(), "/", "-"),
		APIToken:       "step2-test-token-second",
		CaptureContent: false,
	})
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	defer secondClient.Close(context.Background())

	if err := client.RegisterEinoTracing(); err != nil {
		t.Fatalf("first RegisterEinoTracing() error = %v", err)
	}
	if err := secondClient.RegisterEinoTracing(); err != nil {
		t.Fatalf("second client RegisterEinoTracing() error = %v", err)
	}
	if err := client.RegisterEinoTracing(); err != nil {
		t.Fatalf("second RegisterEinoTracing() error = %v", err)
	}
	if appendCalls != 1 {
		t.Fatalf("global handler append count = %d, want 1", appendCalls)
	}
}

func TestRegisterEinoTracingDisabledIsNoop(t *testing.T) {
	originalAppend := appendGlobalHandlers
	appendCalls := 0
	appendGlobalHandlers = func(handlers ...callbacks.Handler) {
		appendCalls += len(handlers)
	}
	defer func() { appendGlobalHandlers = originalAppend }()

	client, err := New(config.CozeLoopConfig{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.RegisterEinoTracing(); err != nil {
		t.Fatalf("RegisterEinoTracing() error = %v", err)
	}
	if appendCalls != 0 {
		t.Fatalf("global handler append count = %d, want 0", appendCalls)
	}
}

func TestMetadataOnlyParserRemovesContentButKeepsModelMetadata(t *testing.T) {
	parser := newTraceDataParser(false)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}

	inputTags := parser.ParseInput(context.Background(), info, &model.CallbackInput{
		Messages: []*schema.Message{schema.UserMessage("private prompt")},
		Config:   &model.Config{Model: "model-name"},
		Extra:    map[string]any{"private": "metadata"},
	})
	if _, ok := inputTags["input"]; ok {
		t.Fatal("metadata-only input contains input content")
	}
	if _, ok := inputTags["extra"]; ok {
		t.Fatal("metadata-only input contains extra content")
	}
	if got := inputTags["model_name"]; got != "model-name" {
		t.Fatalf("model_name = %v, want model-name", got)
	}
	if got := inputTags["model_provider"]; got != "test-model" {
		t.Fatalf("model_provider = %v, want test-model", got)
	}

	outputTags := parser.ParseOutput(context.Background(), info, &model.CallbackOutput{
		Message:    schema.AssistantMessage("private answer", nil),
		TokenUsage: &model.TokenUsage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8},
		Extra:      map[string]any{"private": "output metadata"},
	})
	if _, ok := outputTags["output"]; ok {
		t.Fatal("metadata-only output contains output content")
	}
	if _, ok := outputTags["extra"]; ok {
		t.Fatal("metadata-only output contains extra content")
	}
	if got := outputTags["input_tokens"]; got != 3 {
		t.Fatalf("input_tokens = %v, want 3", got)
	}
	if got := outputTags["output_tokens"]; got != 5 {
		t.Fatalf("output_tokens = %v, want 5", got)
	}
	if got := outputTags["tokens"]; got != 8 {
		t.Fatalf("tokens = %v, want 8", got)
	}
}

func TestMetadataOnlyParserSanitizesErrorText(t *testing.T) {
	filtered := filterTraceMetadata(map[string]any{
		"error":                  "request failed with private prompt and secret-token",
		"model_name":             "model-name",
		"prompt_fallback":        true,
		"prompt_fallback_reason": "get_prompt",
		"input":                  "private prompt",
		"extra":                  "private metadata",
	})
	if filtered["error"] != "error" {
		t.Fatalf("sanitized error = %v, want generic error marker", filtered["error"])
	}
	if filtered["model_name"] != "model-name" {
		t.Fatalf("model_name = %v, want model-name", filtered["model_name"])
	}
	if filtered["prompt_fallback"] != true || filtered["prompt_fallback_reason"] != "get_prompt" {
		t.Fatalf("fallback metadata = %#v", filtered)
	}
	if _, ok := filtered["input"]; ok {
		t.Fatal("filtered metadata retained input content")
	}
	if _, ok := filtered["extra"]; ok {
		t.Fatal("filtered metadata retained extra content")
	}
}

func TestConfiguredTraceDataParserAddsEvaluationMetadataWithoutContent(t *testing.T) {
	parser := newConfiguredTraceDataParser(config.CozeLoopConfig{CaptureContent: false})
	capture := eval.NewTraceCapture(eval.TraceMetadata{
		RunID:         "run-1",
		CaseID:        "case-1",
		CaseVersion:   1,
		CaseTags:      []string{"smoke", "retrieval"},
		RepeatIndex:   2,
		Role:          "candidate",
		GitCommit:     "abc123",
		Model:         "candidate-model",
		PromptKey:     "agent-prompt",
		PromptVersion: "v1",
		PromptSource:  "cozeloop",
	})
	capture.SetPromptResolution(eval.PromptResolution{
		Requested:   eval.PromptSelection{Key: "requested-key", Version: "v1", Source: "cozeloop"},
		Resolved:    eval.PromptSelection{Key: "resolved-key", Version: "v1", Source: "cozeloop"},
		ContentHash: "sha256:abc",
	})
	ctx := eval.WithTraceCapture(context.Background(), capture)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}
	tags := parser.ParseInput(ctx, info, &model.CallbackInput{
		Messages: []*schema.Message{schema.UserMessage("private prompt")},
	})
	if tags["run_id"] != "run-1" || tags["case_id"] != "case-1" || tags["case_version"] != 1 || tags["repeat_index"] != 2 {
		t.Fatalf("evaluation identity metadata = %#v", tags)
	}
	if tags["case_tags"] != "smoke,retrieval" || tags["eval_role"] != "candidate" || tags["git_commit"] != "abc123" {
		t.Fatalf("evaluation role/tags = %#v", tags)
	}
	if tags["eval_model"] != "candidate-model" || tags["eval_prompt_key"] != "agent-prompt" || tags["eval_prompt_version"] != "v1" || tags["eval_prompt_source"] != "cozeloop" {
		t.Fatalf("evaluation model/Prompt metadata = %#v", tags)
	}
	if tags["prompt_resolved_key"] != "resolved-key" || tags["prompt_resolved_version"] != "v1" || tags["prompt_content_hash"] != "sha256:abc" {
		t.Fatalf("resolved Prompt metadata = %#v", tags)
	}
	if _, ok := tags["input"]; ok {
		t.Fatal("metadata-only evaluation trace contains prompt content")
	}
}

func TestTraceCapturingHandlerCapturesIDFromWrappedHandlerContext(t *testing.T) {
	type traceIDKey struct{}
	delegate := callbacks.NewHandlerBuilder().OnStartFn(func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
		return context.WithValue(ctx, traceIDKey{}, "trace-123")
	}).Build()
	capture := eval.NewTraceCapture(eval.TraceMetadata{RunID: "run-1", Role: "candidate"})
	handler := newTraceCapturingHandler(delegate, func(ctx context.Context) string {
		traceID, _ := ctx.Value(traceIDKey{}).(string)
		return traceID
	})
	handler.OnStart(eval.WithTraceCapture(context.Background(), capture), nil, nil)
	if got := capture.Snapshot().TraceID; got != "trace-123" {
		t.Fatalf("captured trace id = %q, want trace-123", got)
	}
}

func TestConfiguredTraceDataParserStopsContentCaptureAfterConsentRevocation(t *testing.T) {
	consentPath := filepath.Join(t.TempDir(), "consent.json")
	cfg := config.CozeLoopConfig{
		CaptureContent: true,
		ConsentPath:    consentPath,
		WorkspaceID:    "workspace-1",
		APIBaseURL:     "https://api.coze.cn",
	}
	parser := newConfiguredTraceDataParser(cfg)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}
	input := &model.CallbackInput{Messages: []*schema.Message{schema.UserMessage("private prompt content")}}
	if tags := parser.ParseInput(context.Background(), info, input); tags["input"] != nil {
		t.Fatal("content was captured before authorization")
	}
	if err := GrantContentConsent(consentPath, cfg.WorkspaceID, cfg.APIBaseURL, []string{ConsentScopeTraceContent}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if tags := parser.ParseInput(context.Background(), info, input); tags["input"] == nil {
		t.Fatal("authorized content capture did not include callback input")
	}
	if err := RevokeContentConsent(consentPath, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if tags := parser.ParseInput(context.Background(), info, input); tags["input"] != nil {
		t.Fatal("content capture continued after consent revocation")
	}
}

func TestConfiguredTraceDataParserAddsServiceMetadata(t *testing.T) {
	parser := newConfiguredTraceDataParser(config.CozeLoopConfig{
		Environment:    "test",
		ServiceName:    "interview-test",
		CaptureContent: false,
	})
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}
	tags := parser.ParseInput(context.Background(), info, &model.CallbackInput{Config: &model.Config{Model: "model-name"}})
	if tags["service.environment"] != "test" || tags["service.name"] != "interview-test" {
		t.Fatalf("service metadata = %#v", tags)
	}
}

func TestStreamParserRetainsUsageOnlyFinalChunk(t *testing.T) {
	parser := newTraceDataParser(false)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}
	reader, writer := schema.Pipe[callbacks.CallbackOutput](2)
	writer.Send(&model.CallbackOutput{
		Message: schema.AssistantMessage("partial", nil),
	}, nil)
	writer.Send(&model.CallbackOutput{
		Message: &schema.Message{
			Role: schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{
				PromptTokens:     11,
				CompletionTokens: 7,
				TotalTokens:      18,
			}},
		},
	}, nil)
	writer.Close()

	tags := parser.ParseStreamOutput(context.Background(), info, reader)
	if got := tags["input_tokens"]; got != 11 {
		t.Fatalf("input_tokens = %v, want 11", got)
	}
	if got := tags["output_tokens"]; got != 7 {
		t.Fatalf("output_tokens = %v, want 7", got)
	}
	if got := tags["tokens"]; got != 18 {
		t.Fatalf("tokens = %v, want 18", got)
	}
	if _, ok := tags["output"]; ok {
		t.Fatal("metadata-only stream output contains output content")
	}
}

func TestCaptureContentParserKeepsPayloads(t *testing.T) {
	parser := newTraceDataParser(true)
	info := &callbacks.RunInfo{Component: components.ComponentOfChatModel, Type: "test-model"}
	tags := parser.ParseInput(context.Background(), info, &model.CallbackInput{
		Messages: []*schema.Message{schema.UserMessage("visible prompt")},
	})
	if _, ok := tags["input"]; !ok {
		t.Fatal("capture-content parser removed input payload")
	}
}

var _ cozeloopcallback.CallbackDataParser = metadataOnlyDataParser{}
