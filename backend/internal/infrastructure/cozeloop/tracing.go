package cozeloop

import (
	"context"
	"errors"
	"sync"

	cozeloopcallback "github.com/cloudwego/eino-ext/callbacks/cozeloop"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
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
		appendGlobalHandlers(handler)
	})
	return nil
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
	return configuredTraceDataParser{
		delegate:    newTraceDataParser(cfg.CaptureContent),
		environment: cfg.Environment,
		serviceName: cfg.ServiceName,
	}
}

type configuredTraceDataParser struct {
	delegate    cozeloopcallback.CallbackDataParser
	environment string
	serviceName string
}

func (p configuredTraceDataParser) ParseInput(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) map[string]any {
	return p.addServiceMetadata(p.delegate.ParseInput(ctx, info, input))
}

func (p configuredTraceDataParser) ParseOutput(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) map[string]any {
	return p.addServiceMetadata(p.delegate.ParseOutput(ctx, info, output))
}

func (p configuredTraceDataParser) ParseStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) map[string]any {
	return p.addServiceMetadata(p.delegate.ParseStreamInput(ctx, info, input))
}

func (p configuredTraceDataParser) ParseStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) map[string]any {
	return p.addServiceMetadata(p.delegate.ParseStreamOutput(ctx, info, output))
}

func (p configuredTraceDataParser) addServiceMetadata(tags map[string]any) map[string]any {
	if tags == nil {
		tags = make(map[string]any)
	} else {
		copied := make(map[string]any, len(tags)+2)
		for key, value := range tags {
			copied[key] = value
		}
		tags = copied
	}
	if p.environment != "" {
		tags["service.environment"] = p.environment
	}
	if p.serviceName != "" {
		tags["service.name"] = p.serviceName
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
	"prompt_fallback":         {},
	"prompt_fallback_reason":  {},
	"prompt_version":          {},
	"reasoning_duration":      {},
	"reasoning_tokens":        {},
	"retriever_provider":      {},
	"run_mode":                {},
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
