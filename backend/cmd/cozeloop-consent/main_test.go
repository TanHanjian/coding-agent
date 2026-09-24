package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/infrastructure/cozeloop"
)

func TestConsentCLIGrantsAndRevokesExplicitScopes(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("APP_DATA_DIR", dataDir)
	t.Setenv("COZELOOP_WORKSPACE_ID", "workspace-test")
	t.Setenv("COZELOOP_API_BASE_URL", "https://api.coze.cn")
	t.Setenv("COZELOOP_API_TOKEN", "must-not-be-written")

	inputPath := filepath.Join(t.TempDir(), "input.txt")
	outputPath := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(inputPath, []byte("I AUTHORIZE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(outputPath)
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := run([]string{"grant", "--scopes", "trace-content,evaluation-dataset-content"}, input, output); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	_ = output.Close()

	consentPath := filepath.Join(dataDir, "settings", "cozeloop-content-consent.json")
	record, err := cozeloop.ReadContentConsent(consentPath)
	if err != nil {
		t.Fatal(err)
	}
	if record.WorkspaceID != "workspace-test" || len(record.Scopes) != 2 {
		t.Fatalf("record = %+v", record)
	}
	contents, err := os.ReadFile(consentPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "must-not-be-written") {
		t.Fatal("consent record contains the CozeLoop API token")
	}

	pendingDir := cozeloop.PrivatePendingDirectory(dataDir)
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "pending.json"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte("REVOKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err = os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err = os.Create(outputPath)
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	if err := run([]string{"revoke"}, input, output); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	_ = output.Close()
	record, err = cozeloop.ReadContentConsent(consentPath)
	if err != nil {
		t.Fatal(err)
	}
	if record.RevokedAt == nil || record.RevokedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("revocation timestamp = %v", record.RevokedAt)
	}
	if _, err := os.Stat(pendingDir); !os.IsNotExist(err) {
		t.Fatalf("pending payload directory remains: %v", err)
	}
}

func TestConsentCLIRequiresExplicitConfirmationAndScopes(t *testing.T) {
	t.Setenv("APP_DATA_DIR", t.TempDir())
	t.Setenv("COZELOOP_WORKSPACE_ID", "workspace-test")
	inputPath := filepath.Join(t.TempDir(), "input.txt")
	outputPath := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(inputPath, []byte("yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"grant", "--scopes", "trace-content"}, input, output); err == nil {
		t.Fatal("grant with generic confirmation should fail")
	}
	_ = input.Close()
	_ = output.Close()
	if _, err := parseScopes(""); err == nil {
		t.Fatal("empty scope list should fail")
	}
	if _, err := parseScopes("trace-content,,evaluation-dataset-content"); err == nil {
		t.Fatal("empty scope entry should fail")
	}
}
