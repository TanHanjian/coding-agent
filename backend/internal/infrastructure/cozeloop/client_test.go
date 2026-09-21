package cozeloop

import (
	"context"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/infrastructure/config"
)

func TestNewDisabledDoesNotRequireCredentials(t *testing.T) {
	client, err := New(config.CozeLoopConfig{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.Enabled() {
		t.Fatal("disabled client reports Enabled() = true")
	}
	if client.sdkClient != nil {
		t.Fatal("disabled client unexpectedly created an SDK client")
	}
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestNewEnabledCreatesClientAndCloseIsIdempotent(t *testing.T) {
	client, err := New(config.CozeLoopConfig{
		Enabled:     true,
		WorkspaceID: "step1-test-workspace-" + strings.ReplaceAll(t.Name(), "/", "-"),
		APIToken:    "step1-test-token",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !client.Enabled() {
		t.Fatal("enabled client reports Enabled() = false")
	}
	if client.sdkClient == nil {
		t.Fatal("enabled client did not create an SDK client")
	}

	ctx := context.Background()
	if err := client.Close(ctx); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestNewEnabledRejectsMissingCredentialsWithoutLeakingToken(t *testing.T) {
	const token = "step1-secret-token"

	client, err := New(config.CozeLoopConfig{
		Enabled:  true,
		APIToken: token,
	})
	if client != nil {
		t.Fatal("New() returned a client for invalid configuration")
	}
	if err == nil {
		t.Fatal("New() error = nil, want missing workspace configuration error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("New() error leaked API token: %v", err)
	}
	if !strings.Contains(err.Error(), "COZELOOP_WORKSPACE_ID") {
		t.Fatalf("New() error = %v, want workspace field name", err)
	}
}

func TestRedact(t *testing.T) {
	const secret = "secret-token"
	got := redact("request failed with secret-token", secret)
	if strings.Contains(got, secret) {
		t.Fatalf("redact() retained secret: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redact() = %q, want redaction marker", got)
	}
}
