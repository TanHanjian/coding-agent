package cozeloop

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/eval"
)

const ContentConsentNoticeVersion = "1"

const (
	ConsentScopeTraceContent      = "trace-content"
	ConsentScopeEvaluationContent = "evaluation-dataset-content"
	consentFileSchemaVersion      = 1
	pendingPayloadSchemaVersion   = 1
	maxPendingPayloadBytes        = 32 << 20
)

const consentPurpose = "CozeLoop Agent trace and evaluation"

var safeRunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

type ContentConsentRecord struct {
	SchemaVersion int        `json:"schemaVersion"`
	WorkspaceID   string     `json:"workspaceId"`
	APIBaseURL    string     `json:"apiBaseUrl"`
	Purpose       string     `json:"purpose"`
	Scopes        []string   `json:"scopes"`
	NoticeVersion string     `json:"noticeVersion"`
	GrantedAt     time.Time  `json:"grantedAt"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
}

type PendingDatasetSync struct {
	SchemaVersion int                     `json:"schemaVersion"`
	RunID         string                  `json:"runId"`
	Payload       eval.DatasetSyncPayload `json:"payload"`
}

func RequireContentConsent(recordPath, workspaceID, apiBaseURL, scope string) error {
	record, err := ReadContentConsent(recordPath)
	if err != nil {
		return errors.New("active CozeLoop content authorization is required")
	}
	if record.SchemaVersion != consentFileSchemaVersion || record.NoticeVersion != ContentConsentNoticeVersion || record.Purpose != consentPurpose || record.WorkspaceID != strings.TrimSpace(workspaceID) || normalizeConsentURL(record.APIBaseURL) != normalizeConsentURL(apiBaseURL) || record.GrantedAt.IsZero() || record.RevokedAt != nil {
		return errors.New("active CozeLoop content authorization does not match this configuration")
	}
	for _, grantedScope := range record.Scopes {
		if grantedScope == scope {
			return nil
		}
	}
	return errors.New("active CozeLoop content authorization does not include the required scope")
}

func ReadContentConsent(path string) (ContentConsentRecord, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return ContentConsentRecord{}, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization path is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return ContentConsentRecord{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ContentConsentRecord{}, errors.New("cannot inspect CozeLoop content authorization")
	}
	if !info.Mode().IsRegular() || !os.SameFile(pathInfo, info) {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization changed while opening")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization file permissions are too broad")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return ContentConsentRecord{}, errors.New("cannot read CozeLoop content authorization")
	}
	if len(data) > 64<<10 {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization exceeds the size limit")
	}
	var record ContentConsentRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization has trailing data")
	}
	if record.SchemaVersion != consentFileSchemaVersion || strings.TrimSpace(record.WorkspaceID) == "" || strings.TrimSpace(record.APIBaseURL) == "" || record.Purpose != consentPurpose || record.GrantedAt.IsZero() || record.NoticeVersion == "" || len(record.Scopes) == 0 {
		return ContentConsentRecord{}, errors.New("CozeLoop content authorization is incomplete")
	}
	return record, nil
}

func GrantContentConsent(recordPath, workspaceID, apiBaseURL string, scopes []string, now time.Time) error {
	workspaceID = strings.TrimSpace(workspaceID)
	apiBaseURL = normalizeConsentURL(apiBaseURL)
	if workspaceID == "" || apiBaseURL == "" || len(scopes) == 0 || now.IsZero() {
		return errors.New("workspace, API endpoint, scopes, and grant time are required")
	}
	scopeSet := make(map[string]struct{}, len(scopes))
	cleanScopes := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != ConsentScopeTraceContent && scope != ConsentScopeEvaluationContent {
			return errors.New("unknown CozeLoop content authorization scope")
		}
		if _, exists := scopeSet[scope]; !exists {
			scopeSet[scope] = struct{}{}
			cleanScopes = append(cleanScopes, scope)
		}
	}
	sort.Strings(cleanScopes)
	return writeJSONPrivate(recordPath, ContentConsentRecord{
		SchemaVersion: consentFileSchemaVersion,
		WorkspaceID:   workspaceID,
		APIBaseURL:    apiBaseURL,
		Purpose:       consentPurpose,
		Scopes:        cleanScopes,
		NoticeVersion: ContentConsentNoticeVersion,
		GrantedAt:     now.UTC(),
	})
}

func RevokeContentConsent(recordPath, pendingDir string, now time.Time) error {
	if now.IsZero() {
		return errors.New("revocation time is required")
	}
	var recordErr error
	if record, err := ReadContentConsent(recordPath); err == nil {
		revokedAt := now.UTC()
		record.RevokedAt = &revokedAt
		if err := writeJSONPrivate(recordPath, record); err != nil {
			if removeErr := os.Remove(recordPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				recordErr = errors.New("cannot disable invalid CozeLoop content authorization")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		if removeErr := os.Remove(recordPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			recordErr = errors.New("cannot disable invalid CozeLoop content authorization")
		}
	}
	if pendingDir != "" {
		info, err := os.Lstat(pendingDir)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			if removeErr := os.Remove(pendingDir); removeErr != nil {
				return errors.New("cannot remove unsafe pending CozeLoop data link")
			}
			return errors.New("pending CozeLoop data directory was a symlink; its target was not traversed and requires manual review")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot inspect pending CozeLoop content directory")
		}
		if err == nil && !info.IsDir() {
			return errors.New("pending CozeLoop content path is not a directory")
		}
		if err == nil {
			if err := validatePrivateDirectory(filepath.Dir(pendingDir)); err != nil {
				return errors.New("pending CozeLoop parent directory is unsafe; payload cleanup was not attempted")
			}
		}
		if err := os.RemoveAll(pendingDir); err != nil {
			return errors.New("cannot remove pending CozeLoop content payloads")
		}
	}
	return recordErr
}

func PendingDatasetSyncPath(pendingDir, runID string) (string, error) {
	if pendingDir == "" || !safeRunIDPattern.MatchString(runID) {
		return "", errors.New("pending sync directory or run ID is invalid")
	}
	return filepath.Join(pendingDir, runID+".json"), nil
}

func SavePendingDatasetSync(pendingDir string, payload PendingDatasetSync) (string, error) {
	if payload.SchemaVersion == 0 {
		payload.SchemaVersion = pendingPayloadSchemaVersion
	}
	expectedVersion, versionErr := eval.EvaluationDatasetVersionForRun(payload.RunID)
	if payload.SchemaVersion != pendingPayloadSchemaVersion || !safeRunIDPattern.MatchString(payload.RunID) || versionErr != nil || payload.Payload.RunID != payload.RunID || payload.Payload.Version.Version != expectedVersion || len(payload.Payload.Items) == 0 {
		return "", errors.New("pending evaluation sync payload is invalid")
	}
	for _, item := range payload.Payload.Items {
		if !strings.HasPrefix(item.Identity, payload.RunID+":") {
			return "", errors.New("pending evaluation sync item does not match its run ID")
		}
	}
	path, err := PendingDatasetSyncPath(pendingDir, payload.RunID)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("cannot encode pending evaluation sync payload")
	}
	if len(encoded) > maxPendingPayloadBytes {
		return "", errors.New("pending evaluation sync payload exceeds the size limit")
	}
	if existing, readErr := ReadPendingDatasetSync(path); readErr == nil {
		existingBytes, _ := json.Marshal(existing)
		oldHash := sha256.Sum256(existingBytes)
		newHash := sha256.Sum256(encoded)
		if oldHash != newHash {
			return "", errors.New("pending evaluation sync payload already exists with different content")
		}
		return path, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", readErr
	}
	if err := writeBytesPrivate(path, encoded); err != nil {
		return "", err
	}
	return path, nil
}

func ReadPendingDatasetSync(path string) (PendingDatasetSync, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return PendingDatasetSync{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxPendingPayloadBytes || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload is unsafe or too large")
	}
	if err := validatePrivateDirectory(filepath.Dir(path)); err != nil {
		return PendingDatasetSync{}, errors.New("pending evaluation sync directory is unsafe")
	}
	if err := validatePrivateDirectory(filepath.Dir(filepath.Dir(path))); err != nil {
		return PendingDatasetSync{}, errors.New("pending evaluation sync parent directory is unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return PendingDatasetSync{}, errors.New("cannot read pending evaluation sync payload")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() > maxPendingPayloadBytes || (runtime.GOOS != "windows" && openedInfo.Mode().Perm()&0o077 != 0) {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload changed or is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPendingPayloadBytes+1))
	if err != nil {
		return PendingDatasetSync{}, errors.New("cannot read pending evaluation sync payload")
	}
	if len(data) > maxPendingPayloadBytes {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload exceeds the size limit")
	}
	var pending PendingDatasetSync
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pending); err != nil {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload has trailing data")
	}
	expectedVersion, versionErr := eval.EvaluationDatasetVersionForRun(pending.RunID)
	if pending.SchemaVersion != pendingPayloadSchemaVersion || !safeRunIDPattern.MatchString(pending.RunID) || versionErr != nil || len(pending.Payload.Items) == 0 {
		return PendingDatasetSync{}, errors.New("pending evaluation sync payload is incomplete")
	}
	if pending.Payload.RunID == "" {
		pending.Payload.RunID = pending.RunID
	}
	if pending.Payload.RunID != pending.RunID {
		return PendingDatasetSync{}, errors.New("pending evaluation sync run IDs do not match")
	}
	switch pending.Payload.Version.Version {
	case expectedVersion:
	case "run-" + pending.RunID:
		pending.Payload.LegacyVersion = pending.Payload.Version.Version
		pending.Payload.Version.Version = expectedVersion
	default:
		return PendingDatasetSync{}, errors.New("pending evaluation sync version does not match its run ID")
	}
	for _, item := range pending.Payload.Items {
		if !strings.HasPrefix(item.Identity, pending.RunID+":") || item.ItemKey == "" || item.ContentHash == "" {
			return PendingDatasetSync{}, errors.New("pending evaluation sync item is invalid")
		}
	}
	return pending, nil
}

func DeletePendingDatasetSync(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot delete pending CozeLoop content payload")
	}
	return nil
}

func PrivatePendingDirectory(dataDir string) string {
	return filepath.Join(dataDir, "cozeloop", "pending")
}

func normalizeConsentURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func writeJSONPrivate(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("cannot encode CozeLoop content authorization")
	}
	return writeBytesPrivate(path, encoded)
}

func writeBytesPrivate(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errors.New("cannot create private CozeLoop data directory")
	}
	if err := validatePrivateDirectory(directory); err != nil {
		return errors.New("private CozeLoop data directory is unsafe")
	}
	if err := validatePrivateDirectory(filepath.Dir(directory)); err != nil {
		return errors.New("private CozeLoop parent directory is unsafe")
	}
	_ = os.Chmod(directory, 0o700)
	temporary, err := os.CreateTemp(directory, ".cozeloop-private-*.tmp")
	if err != nil {
		return errors.New("cannot create private CozeLoop data file")
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return errors.New("cannot restrict CozeLoop data file permissions")
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return errors.New("cannot write CozeLoop data file")
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return errors.New("cannot sync CozeLoop data file")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("cannot close CozeLoop data file")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("cannot atomically replace CozeLoop data file: %s", safeFileError(err))
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return errors.New("cannot restrict CozeLoop data file permissions")
		}
	}
	return nil
}

func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a regular directory")
	}
	return nil
}

func safeFileError(err error) string {
	if errors.Is(err, os.ErrPermission) {
		return "permission denied"
	}
	return "filesystem error"
}
