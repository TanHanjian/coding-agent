package cozeloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/eval"
)

func TestContentConsentRequiresMatchingWorkspaceEndpointAndScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "consent.json")
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := GrantContentConsent(path, "workspace-1", "https://api.coze.cn/", []string{
		ConsentScopeEvaluationContent,
		ConsentScopeTraceContent,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := RequireContentConsent(path, "workspace-1", "https://api.coze.cn", ConsentScopeEvaluationContent); err != nil {
		t.Fatalf("matching consent rejected: %v", err)
	}
	if err := RequireContentConsent(path, "workspace-2", "https://api.coze.cn", ConsentScopeEvaluationContent); err == nil {
		t.Fatal("workspace mismatch was authorized")
	}
	if err := RequireContentConsent(path, "workspace-1", "https://api.coze.com", ConsentScopeEvaluationContent); err == nil {
		t.Fatal("API endpoint mismatch was authorized")
	}
	if err := RequireContentConsent(path, "workspace-1", "https://api.coze.cn", "unlisted-scope"); err == nil {
		t.Fatal("unlisted scope was authorized")
	}
	record, err := ReadContentConsent(path)
	if err != nil {
		t.Fatal(err)
	}
	if record.Purpose == "" || record.NoticeVersion != ContentConsentNoticeVersion || !record.GrantedAt.Equal(now) || strings.Join(record.Scopes, ",") != ConsentScopeEvaluationContent+","+ConsentScopeTraceContent {
		t.Fatalf("consent record = %+v", record)
	}
}

func TestRevokingContentConsentDeletesPendingPayloads(t *testing.T) {
	dir := t.TempDir()
	consentPath := filepath.Join(dir, "consent.json")
	pendingDir := filepath.Join(dir, "pending")
	if err := GrantContentConsent(consentPath, "workspace", "https://api.coze.cn", []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, "pending.json"), []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RevokeContentConsent(consentPath, pendingDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pendingDir); !os.IsNotExist(err) {
		t.Fatalf("pending directory still exists: %v", err)
	}
	if err := RequireContentConsent(consentPath, "workspace", "https://api.coze.cn", ConsentScopeEvaluationContent); err == nil {
		t.Fatal("revoked consent was still accepted")
	}
}

func TestPendingDatasetSyncMigratesLegacyVersionNames(t *testing.T) {
	pendingDir := filepath.Join(t.TempDir(), "cozeloop", "pending")
	path, err := PendingDatasetSyncPath(pendingDir, "run_legacy")
	if err != nil {
		t.Fatal(err)
	}
	legacy := PendingDatasetSync{
		SchemaVersion: pendingPayloadSchemaVersion,
		RunID:         "run_legacy",
		Payload: eval.DatasetSyncPayload{
			Dataset: eval.DefaultEvaluationDatasetSpec(),
			Version: eval.DatasetVersionSpec{Version: "run-run_legacy"},
			Items: []eval.DatasetItem{{
				ItemKey: "result-1", Identity: "run_legacy:case-1:1:1", ContentHash: "hash-1",
				Fields: map[string]string{"result_identity": "run_legacy:case-1:1:1"},
			}},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBytesPrivate(path, data); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadPendingDatasetSync(path)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion, err := eval.EvaluationDatasetVersionForRun("run_legacy")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Payload.RunID != legacy.RunID || loaded.Payload.Version.Version != wantVersion || loaded.Payload.LegacyVersion != legacy.Payload.Version.Version {
		t.Fatalf("legacy pending payload was not normalized: %+v", loaded.Payload)
	}
}

func TestPendingDatasetSyncRefusesSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	pendingDir := filepath.Join(root, "pending")
	if err := os.Symlink(target, pendingDir); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	version, err := eval.EvaluationDatasetVersionForRun("symlink-run")
	if err != nil {
		t.Fatal(err)
	}
	payload := PendingDatasetSync{
		RunID: "symlink-run",
		Payload: eval.DatasetSyncPayload{
			RunID: "symlink-run", Dataset: eval.DefaultEvaluationDatasetSpec(),
			Version: eval.DatasetVersionSpec{Version: version},
			Items:   []eval.DatasetItem{{ItemKey: "item-1", Identity: "symlink-run:case:1:1", ContentHash: "hash"}},
		},
	}
	if _, err := SavePendingDatasetSync(pendingDir, payload); err == nil {
		t.Fatal("pending payload was written through a symlink directory")
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("symlink target received pending data: %v", entries)
	}
}

func TestRevokingContentConsentDoesNotFollowPendingSymlink(t *testing.T) {
	root := t.TempDir()
	consentPath := filepath.Join(root, "settings", "consent.json")
	parent := filepath.Join(root, "cozeloop")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	pendingDir := filepath.Join(parent, "pending")
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(target, "run.json")
	if err := os.WriteFile(payloadPath, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, pendingDir); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	if err := GrantContentConsent(consentPath, "workspace", "https://api.coze.cn", []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RevokeContentConsent(consentPath, pendingDir, time.Now()); err == nil {
		t.Fatal("revocation reported success without cleaning an unknown symlink target")
	}
	if _, err := os.Lstat(pendingDir); !os.IsNotExist(err) {
		t.Fatalf("pending symlink remains: %v", err)
	}
	if _, err := os.Stat(payloadPath); err != nil {
		t.Fatalf("unknown symlink target was followed or removed: %v", err)
	}
	if err := RequireContentConsent(consentPath, "workspace", "https://api.coze.cn", ConsentScopeEvaluationContent); err == nil {
		t.Fatal("consent remained active after revocation")
	}
}

func TestReadPendingDatasetSyncRejectsNonRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPendingDatasetSync(path); err == nil {
		t.Fatal("non-regular pending payload path was accepted")
	}
}

func TestPendingDatasetSyncIsPrivateImmutableAndReusable(t *testing.T) {
	pendingDir := filepath.Join(t.TempDir(), "cozeloop", "pending")
	version, err := eval.EvaluationDatasetVersionForRun("run-123")
	if err != nil {
		t.Fatal(err)
	}
	payload := PendingDatasetSync{
		SchemaVersion: pendingPayloadSchemaVersion,
		RunID:         "run-123",
		Payload: eval.DatasetSyncPayload{
			RunID:   "run-123",
			Dataset: eval.DefaultEvaluationDatasetSpec(),
			Version: eval.DatasetVersionSpec{Version: version},
			Items: []eval.DatasetItem{{
				ItemKey:     "result-1",
				Identity:    "run-123:case-1:1:1",
				ContentHash: "hash-1",
				Fields:      map[string]string{"actual_output": "sensitive answer"},
			}},
		},
	}
	path, err := SavePendingDatasetSync(pendingDir, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join("pending", "run-123.json")) {
		t.Fatalf("pending path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("pending file permissions = %o, want 600", info.Mode().Perm())
	}
	if _, err := SavePendingDatasetSync(pendingDir, payload); err != nil {
		t.Fatalf("identical retry could not reuse pending payload: %v", err)
	}
	loaded, err := ReadPendingDatasetSync(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != payload.RunID || loaded.Payload.Items[0].Fields["actual_output"] != "sensitive answer" {
		t.Fatalf("loaded pending payload = %+v", loaded)
	}
	payload.Payload.Items[0].Fields["actual_output"] = "changed answer"
	if _, err := SavePendingDatasetSync(pendingDir, payload); err == nil {
		t.Fatal("changed result overwrote an existing pending payload")
	}
	if err := DeletePendingDatasetSync(path); err != nil {
		t.Fatal(err)
	}
}

func TestContentConsentFailsClosedForInvalidRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consent.json")
	if err := os.WriteFile(path, []byte(`{"workspaceId":"workspace"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireContentConsent(path, "workspace", "https://api.coze.cn", ConsentScopeTraceContent); err == nil {
		t.Fatal("incomplete consent record was accepted")
	}
	if err := GrantContentConsent(path, "workspace", "https://api.coze.cn", []string{"all"}, time.Now()); err == nil {
		t.Fatal("unknown scope was accepted")
	}
}
