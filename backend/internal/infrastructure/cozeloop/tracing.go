package cozeloop

import (
	"context"
	"errors"
	"strings"
	"sync"

	cozeloopcallback "github.com/cloudwego/eino-ext/callbacks/cozeloop"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

var (
	appendGlobalHandlers = callbacks.AppendGlobalHandlers
	registerGlobalOnce   sync.Once
)

// RegisterEinoTracing installs the CozeLoop Eino callback once for the
// process. Global callback registration is intentionally kept here, rather
// than in the Agent or Executor, so server and eval use the same lifecycle
// boundary and requests do not append duplicate handlers.
func (c *Client) RegisterEinoTracing() error {
	if c == nil || !c.Enabled() {
		return nil
	}
	if c.sdkClient == nil {
		return errors.New("CozeLoop tracing: SDK client is not initialized")
	}

	registerGlobalOnce.Do(func() {
		handler := cozeloopcallback.NewLoopHandler(
			c.sdkClient,
			cozeloopcallback.WithCallbackDataParser(newConfiguredTraceDataParser(c.cfg)),
		)
		appendGlobalHandlers(newTraceCapturingHandler(handler, func(ctx context.Context) string {
			return cozeloopcallback.GetSpanContext(ctx).GetTraceID()
		}))
	})
	return nil
}

type traceCapturingHandler struct {
	delegate  callbacks.Handler
	traceIDOf func(context.Context) string
}

func newTraceCapturingHandler(delegate callbacks.Handler, traceIDOf func(context.Context) string) callbacks.Handler {
	return &traceCapturingHandler{delegate: delegate, traceIDOf: traceIDOf}
}

func (h *traceCapturingHandler) capture(ctx context.Context) {
	if h == nil || h.traceIDOf == nil {
		return
	}
	capture := eval.TraceCaptureFromContext(ctx)
	if capture != nil {
		capture.SetTraceID(h.traceIDOf(ctx))
	}
}

func (h *traceCapturingHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if h == nil || h.delegate == nil {
		return ctx
	}
	ctx = h.delegate.OnStart(ctx, info, input)
	h.capture(ctx)
	return ctx
}

func (h *traceCapturingHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if h == nil || h.delegate == nil {
		return ctx
	}
	ctx = h.delegate.OnEnd(ctx, info, output)
	h.capture(ctx)
	return ctx
}

func (h *traceCapturingHandler) OnError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if h == nil || h.delegate == nil {
		return ctx
	}
	ctx = h.delegate.OnError(ctx, info, err)
	h.capture(ctx)
	return ctx
}

func (h *traceCapturingHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if h == nil || h.delegate == nil {
		if input != nil {
			input.Close()
		}
		return ctx
	}
	ctx = h.delegate.OnStartWithStreamInput(ctx, info, input)
	h.capture(ctx)
	return ctx
}

func (h *traceCapturingHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if h == nil || h.delegate == nil {
		if output != nil {
			output.Close()
		}
		return ctx
	}
	ctx = h.delegate.OnEndWithStreamOutput(ctx, info, output)
	h.capture(ctx)
	return ctx
}

// newTraceDataParser keeps the upstream parser's token/model/error extraction
// while making content collection explicit. CozeLoop's default parser includes
// input and output payloads, so metadata-only mode uses an allowlist instead of
// relying on callers to remember which fields may contain user data.
func newTraceDataParser(captureContent bool) cozeloopcallback.CallbackDataParser {
	parser := cozeloopcallback.NewDefaultDataParser(false)
	if captureContent {
		return parser
	}
	return metadataOnlyDataParser{delegate: parser}
}

func newConfiguredTraceDataParser(cfg config.CozeLoopConfig) cozeloopcallback.CallbackDataParser {
	return configuredTraceDataParser{config: cfg}
}

type configuredTraceDataParser struct {
	config config.CozeLoopConfig
}

func (p configuredTraceDataParser) delegate() cozeloopcallback.CallbackDataParser {
	captureContent := p.config.CaptureContent && RequireContentConsent(
		p.config.ConsentPath,
		p.config.WorkspaceID,
		p.config.APIBaseURL,
		ConsentScopeTraceContent,
	) == nil
	return newTraceDataParser(captureContent)
}

func (p configuredTraceDataParser) ParseInput(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) map[string]any {
	return p.addServiceMetadata(ctx, p.delegate().ParseInput(ctx, info, input))
}

func (p configuredTraceDataParser) ParseOutput(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) map[string]any {
	return p.addServiceMetadata(ctx, p.delegate().ParseOutput(ctx, info, output))
}

func (p configuredTraceDataParser) ParseStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) map[string]any {
	return p.addServiceMetadata(ctx, p.delegate().ParseStreamInput(ctx, info, input))
}

func (p configuredTraceDataParser) ParseStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) map[string]any {
	return p.addServiceMetadata(ctx, p.delegate().ParseStreamOutput(ctx, info, output))
}

func (p configuredTraceDataParser) addServiceMetadata(ctx context.Context, tags map[string]any) map[string]any {
	if tags == nil {
		tags = make(map[string]any)
	} else {
		copied := make(map[string]any, len(tags)+2)
		for key, value := range tags {
			copied[key] = value
		}
		tags = copied
	}
	if p.config.Environment != "" {
		tags["service.environment"] = p.config.Environment
	}
	if p.config.ServiceName != "" {
		tags["service.name"] = p.config.ServiceName
	}
	if capture := eval.TraceCaptureFromContext(ctx); capture != nil {
		metadata := capture.Metadata()
		if metadata.RunID != "" {
			tags["run_id"] = metadata.RunID
		}
		if metadata.CaseID != "" {
			tags["case_id"] = metadata.CaseID
		}
		if metadata.CaseVersion > 0 {
			tags["case_version"] = metadata.CaseVersion
		}
		if metadata.RepeatIndex > 0 {
			tags["repeat_index"] = metadata.RepeatIndex
		}
		if len(metadata.CaseTags) > 0 {
			tags["case_tags"] = strings.Join(metadata.CaseTags, ",")
		}
		if metadata.Role != "" {
			tags["eval_role"] = metadata.Role
		}
		if metadata.GitCommit != "" {
			tags["git_commit"] = metadata.GitCommit
		}
		if metadata.Model != "" {
			tags["eval_model"] = metadata.Model
		}
		if metadata.PromptKey != "" {
			tags["eval_prompt_key"] = metadata.PromptKey
		}
		if metadata.PromptVersion != "" {
			tags["eval_prompt_version"] = metadata.PromptVersion
		}
		if metadata.PromptLabel != "" {
			tags["eval_prompt_label"] = metadata.PromptLabel
		}
		if metadata.PromptSource != "" {
			tags["eval_prompt_source"] = metadata.PromptSource
		}
		if prompt := capture.PromptResolution(); prompt != nil {
			if prompt.Resolved.Key != "" {
				tags["prompt_resolved_key"] = prompt.Resolved.Key
			}
			if prompt.Resolved.Version != "" {
				tags["prompt_resolved_version"] = prompt.Resolved.Version
			}
			if prompt.Resolved.Label != "" {
				tags["prompt_resolved_label"] = prompt.Resolved.Label
			}
			if prompt.Resolved.Source != "" {
				tags["prompt_resolved_source"] = prompt.Resolved.Source
			}
			tags["prompt_fallback"] = prompt.Fallback
			if prompt.FallbackReason != "" {
				tags["prompt_fallback_reason"] = prompt.FallbackReason
			}
			if prompt.ContentHash != "" {
				tags["prompt_content_hash"] = prompt.ContentHash
			}
		}
	}
	return tags
}

type metadataOnlyDataParser struct {
	delegate cozeloopcallback.CallbackDataParser
}

func (p metadataOnlyDataParser) ParseInput(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) map[string]any {
	return filterTraceMetadata(p.delegate.ParseInput(ctx, info, input))
}

func (p metadataOnlyDataParser) ParseOutput(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) map[string]any {
	return filterTraceMetadata(p.delegate.ParseOutput(ctx, info, output))
}

func (p metadataOnlyDataParser) ParseStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) map[string]any {
	return filterTraceMetadata(p.delegate.ParseStreamInput(ctx, info, input))
}

func (p metadataOnlyDataParser) ParseStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) map[string]any {
	return filterTraceMetadata(p.delegate.ParseStreamOutput(ctx, info, output))
}

// These are the public CozeLoop trace keys retained in metadata-only mode.
// Values under input/output/extra are intentionally excluded because they may
// contain prompts, model text, tool arguments, tool results, or fixture data.
var traceMetadataKeys = map[string]struct{}{
	"agent_name":              {},
	"call_options":            {},
	"call_type":               {},
	"eino_run_info_component": {},
	"eino_run_info_name":      {},
	"eino_run_info_type":      {},
	"error":                   {},
	"es_cluster":              {},
	"es_index":                {},
	"es_name":                 {},
	"input_tokens":            {},
	"latency_first_resp":      {},
	"log_id":                  {},
	"model_identification":    {},
	"model_name":              {},
	"model_platform":          {},
	"model_provider":          {},
	"output_tokens":           {},
	"prompt_key":              {},
	"prompt_label":            {},
	"prompt_provider":         {},
	"prompt_resolved_key":     {},
	"prompt_resolved_version": {},
	"prompt_resolved_label":   {},
	"prompt_resolved_source":  {},
	"prompt_content_hash":     {},
	"prompt_fallback":         {},
	"prompt_fallback_reason":  {},
	"prompt_version":          {},
	"reasoning_duration":      {},
	"reasoning_tokens":        {},
	"retriever_provider":      {},
	"run_mode":                {},
	"run_id":                  {},
	"case_id":                 {},
	"case_version":            {},
	"case_tags":               {},
	"repeat_index":            {},
	"eval_role":               {},
	"eval_model":              {},
	"eval_prompt_key":         {},
	"eval_prompt_version":     {},
	"eval_prompt_label":       {},
	"eval_prompt_source":      {},
	"git_commit":              {},
	"stream":                  {},
	"tokens":                  {},
	"tool_call_id":            {},
	"vikingdb_name":           {},
	"vikingdb_region":         {},
}

func filterTraceMetadata(tags map[string]any) map[string]any {
	if tags == nil {
		return nil
	}
	filtered := make(map[string]any, len(tags))
	for key, value := range tags {
		if _, ok := traceMetadataKeys[key]; !ok {
			continue
		}
		if key == "error" {
			// Keep the fact that an error occurred without exporting arbitrary
			// SDK/model/tool error text, which may contain user data or secrets.
			filtered[key] = "error"
			continue
		}
		filtered[key] = value
	}
	return filtered
}
