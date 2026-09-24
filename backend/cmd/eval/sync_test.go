package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"interview-memory-agent/backend/internal/eval"
)

type fakeEvaluationPlatform struct {
	dataset    eval.Dataset
	versions   []eval.DatasetVersionRecord
	draftItems map[string]eval.DatasetItem
	snapshots  map[string][]eval.DatasetItem
	failWrite  bool
}

func newFakeEvaluationPlatform() *fakeEvaluationPlatform {
	return &fakeEvaluationPlatform{
		dataset:    eval.Dataset{ID: "dataset-1", Key: eval.EvaluationDatasetKey},
		draftItems: make(map[string]eval.DatasetItem),
		snapshots:  make(map[string][]eval.DatasetItem),
	}
}

func (f *fakeEvaluationPlatform) EnsureDataset(context.Context, eval.DatasetSpec) (eval.Dataset, error) {
	return f.dataset, nil
}

func (f *fakeEvaluationPlatform) ListDatasetVersions(context.Context, string) ([]eval.DatasetVersionRecord, error) {
	return append([]eval.DatasetVersionRecord(nil), f.versions...), nil
}

func (f *fakeEvaluationPlatform) ListDatasetItems(_ context.Context, _, versionID string) ([]eval.DatasetItem, error) {
	if versionID != "" {
		return append([]eval.DatasetItem(nil), f.snapshots[versionID]...), nil
	}
	items := make([]eval.DatasetItem, 0, len(f.draftItems))
	for _, item := range f.draftItems {
		items = append(items, item)
	}
	return items, nil
}

func (f *fakeEvaluationPlatform) BatchCreateDatasetItems(_ context.Context, _ string, items []eval.DatasetItem) ([]eval.ItemWriteResult, error) {
	if f.failWrite {
		return nil, errors.New("simulated platform write failure")
	}
	results := make([]eval.ItemWriteResult, 0, len(items))
	for _, item := range items {
		f.draftItems[item.ItemKey] = item
		results = append(results, eval.ItemWriteResult{ItemKey: item.ItemKey, IsNewItem: true})
	}
	return results, nil
}

func (f *fakeEvaluationPlatform) CreateDatasetVersion(_ context.Context, _ string, spec eval.DatasetVersionSpec) (eval.DatasetVersionRecord, error) {
	version := eval.DatasetVersionRecord{ID: "version-1", Version: spec.Version}
	f.versions = append(f.versions, version)
	items := make([]eval.DatasetItem, 0, len(f.draftItems))
	for _, item := range f.draftItems {
		items = append(items, item)
	}
	f.snapshots[version.ID] = items
	return version, nil
}

func TestPrepareAndSyncDatasetRetainsReportAndDeletesSuccessfulPayload(t *testing.T) {
	dataDir := t.TempDir()
	pendingDir := filepath.Join(dataDir, "pending")
	runID := "run-1"
	reportDir := filepath.Join(t.TempDir(), runID)
	currentCase := eval.EvalCase{ID: "case-1", Version: eval.DatasetVersion, Input: eval.EvalInput{Query: "question"}}
	report := eval.BuildReport("live", []eval.CaseResult{{
		RunID: runID, CaseID: currentCase.ID, CaseVersion: currentCase.Version, RepeatIndex: 1,
		Status: "passed", Answer: "answer",
	}})
	report.RunID = runID
	if err := eval.WriteReport(reportDir, report); err != nil {
		t.Fatal(err)
	}
	client := newFakeEvaluationPlatform()
	if err := prepareAndSyncDataset(context.Background(), pendingDir, reportDir, []eval.EvalCase{currentCase}, report, client); err != nil {
		t.Fatal(err)
	}
	loaded, err := eval.LoadReport(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CozeLoopSync == nil || loaded.CozeLoopSync.Status != "synced" || loaded.CozeLoopSync.PendingPayload {
		t.Fatalf("CozeLoop sync report = %+v", loaded.CozeLoopSync)
	}
	pendingPath, _ := pendingPathForRun(pendingDir, runID)
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending content payload remains after sync: %v", err)
	}
}

type revokedConsentPlatform struct {
	*fakeEvaluationPlatform
}

func (revokedConsentPlatform) BatchCreateDatasetItems(context.Context, string, []eval.DatasetItem) ([]eval.ItemWriteResult, error) {
	return nil, errors.New("CozeLoop evaluation content authorization is no longer active")
}

func TestSyncPendingDatasetDeletesPayloadWhenConsentIsRevoked(t *testing.T) {
	pendingDir := filepath.Join(t.TempDir(), "pending")
	runID := "run-revoked"
	reportDir := filepath.Join(t.TempDir(), runID)
	currentCase := eval.EvalCase{ID: "case-1", Version: eval.DatasetVersion, Input: eval.EvalInput{Query: "question"}}
	report := eval.BuildReport("live", []eval.CaseResult{{
		RunID: runID, CaseID: currentCase.ID, CaseVersion: currentCase.Version, RepeatIndex: 1,
		Status: "passed", Answer: "answer",
	}})
	report.RunID = runID
	if err := eval.WriteReport(reportDir, report); err != nil {
		t.Fatal(err)
	}
	if err := prepareAndSyncDataset(context.Background(), pendingDir, reportDir, []eval.EvalCase{currentCase}, report, revokedConsentPlatform{newFakeEvaluationPlatform()}); err == nil {
		t.Fatal("sync error = nil, want revoked-consent error")
	}
	pendingPath, _ := pendingPathForRun(pendingDir, runID)
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("payload remains after consent revocation: %v", err)
	}
	loaded, err := eval.LoadReport(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CozeLoopSync == nil || loaded.CozeLoopSync.ErrorCategory != "authorization" || loaded.CozeLoopSync.PendingPayload {
		t.Fatalf("revoked-consent report = %+v", loaded.CozeLoopSync)
	}
}

func TestSyncPendingDatasetRetainsPayloadAfterRemoteFailureAndCanResume(t *testing.T) {
	pendingDir := filepath.Join(t.TempDir(), "pending")
	runID := "run-2"
	reportDir := filepath.Join(t.TempDir(), runID)
	currentCase := eval.EvalCase{ID: "case-1", Version: eval.DatasetVersion, Input: eval.EvalInput{Query: "question"}}
	report := eval.BuildReport("live", []eval.CaseResult{{
		RunID: runID, CaseID: currentCase.ID, CaseVersion: currentCase.Version, RepeatIndex: 1,
		Status: "passed", Answer: "answer",
	}})
	report.RunID = runID
	if err := eval.WriteReport(reportDir, report); err != nil {
		t.Fatal(err)
	}
	failing := newFakeEvaluationPlatform()
	failing.failWrite = true
	if err := prepareAndSyncDataset(context.Background(), pendingDir, reportDir, []eval.EvalCase{currentCase}, report, failing); err == nil {
		t.Fatal("initial publish error = nil, want simulated failure")
	}
	pendingPath, _ := pendingPathForRun(pendingDir, runID)
	if _, err := os.Stat(pendingPath); err != nil {
		t.Fatalf("pending payload was not retained: %v", err)
	}
	loaded, err := eval.LoadReport(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CozeLoopSync == nil || loaded.CozeLoopSync.Status != "failed" || !loaded.CozeLoopSync.PendingPayload {
		t.Fatalf("failed sync report = %+v", loaded.CozeLoopSync)
	}
	if err := syncPendingDataset(context.Background(), pendingDir, reportDir, newFakeEvaluationPlatform()); err != nil {
		t.Fatal(err)
	}
	loaded, err = eval.LoadReport(filepath.Join(reportDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CozeLoopSync == nil || loaded.CozeLoopSync.Status != "synced" || loaded.CozeLoopSync.PendingPayload {
		t.Fatalf("resumed sync report = %+v", loaded.CozeLoopSync)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending payload remains after resumed sync: %v", err)
	}
}
