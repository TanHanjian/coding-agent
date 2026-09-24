package cozeloop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	cozeloopsdk "github.com/coze-dev/cozeloop-go"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

// Client owns the process-level CozeLoop SDK client. The SDK client is created
// once during application assembly and closed once during process shutdown.
// When CozeLoop is disabled, Client is a no-op and does not require credentials.
type Client struct {
	cfg       config.CozeLoopConfig
	sdkClient cozeloopsdk.Client
	closeOnce sync.Once
}

// New creates a process-level CozeLoop client from configuration.
// It does not make a network request; the SDK starts reporting only when a
// feature such as tracing or prompt retrieval is attached in a later step.
func New(cfg config.CozeLoopConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate CozeLoop configuration: %w", err)
	}
	if strings.TrimSpace(cfg.APIBaseURL) == "" {
		cfg.APIBaseURL = defaultEvaluationAPIBaseURL
	}

	client := &Client{cfg: cfg}
	if cfg.CaptureContent {
		if err := RequireContentConsent(cfg.ConsentPath, cfg.WorkspaceID, cfg.APIBaseURL, ConsentScopeTraceContent); err != nil {
			return nil, errors.New("COZELOOP_CAPTURE_CONTENT requires active trace-content authorization")
		}
	}
	if !cfg.Enabled {
		return client, nil
	}

	options := []cozeloopsdk.Option{
		cozeloopsdk.WithWorkspaceID(strings.TrimSpace(cfg.WorkspaceID)),
		cozeloopsdk.WithAPIToken(strings.TrimSpace(cfg.APIToken)),
	}
	if strings.TrimSpace(cfg.APIBaseURL) != "" {
		options = append(options, cozeloopsdk.WithAPIBaseURL(strings.TrimSpace(cfg.APIBaseURL)))
	}
	if cfg.PromptCacheSize > 0 {
		options = append(options, cozeloopsdk.WithPromptCacheMaxCount(cfg.PromptCacheSize))
	}
	if cfg.PromptRefreshInterval > 0 {
		options = append(options, cozeloopsdk.WithPromptCacheRefreshInterval(cfg.PromptRefreshInterval))
	}
	// Prompt Hub SDK traces are intentionally kept metadata-only: unlike the
	// Eino callback parser, the SDK has no per-request revocation check for them.
	sdkClient, err := cozeloopsdk.NewClient(options...)
	if err != nil {
		// Do not include the raw SDK error if it happens to echo credentials.
		// Configuration errors have already been validated above, and the SDK
		// does not need to expose its authentication material to callers.
		return nil, fmt.Errorf("create CozeLoop client: %s", redact(err.Error(), cfg.APIToken))
	}
	client.sdkClient = sdkClient
	return client, nil
}

// Enabled reports whether this client was configured to use CozeLoop.
func (c *Client) Enabled() bool {
	return c != nil && c.cfg.Enabled
}

// Close flushes and closes the SDK client. It is safe to call multiple times.
// The SDK Close method does not return an error, so this boundary currently
// returns nil while keeping an error-bearing lifecycle API for future adapters.
func (c *Client) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.closeOnce.Do(func() {
		if c.sdkClient != nil {
			c.sdkClient.Close(ctx)
		}
	})
	return nil
}

func redact(message string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[REDACTED]")
		}
	}
	return message
}
