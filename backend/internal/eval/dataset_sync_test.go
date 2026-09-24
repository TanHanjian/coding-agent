package eval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Dataset mapping tests.
func TestBuildDatasetItemsSeparatesCaseAndExecutionIdentity(t *testing.T) {
	caseData := EvalCase{
		ID:      "case-1",
		Version: DatasetVersion,
		Tags:    []string{"smoke"},
		Input:   EvalInput{Query: "question", InterviewContext: "context"},
		ToolExpectations: ToolExpectations{
			Required: []string{"search_question_memory"},
			Ordered:  []string{"search_question_memory"},
		},
		AnswerExpectations: AnswerExpectations{
			MustContain:    []string{"grounded"},
			MustNotContain: []string{"fabricated claim"},
			CriticalForbid: []string{"API key"},
			JudgeRubric:    "be grounded",
		},
	}
	usage := &TokenUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8}
	result := CaseResult{
		RunID:       "run-1",
		CaseID:      "case-1",
		CaseVersion: DatasetVersion,
		RepeatIndex: 1,
		Tags:        []string{"smoke"},
		Status:      "passed",
		Answer:      "grounded answer",
		Candidate:   TraceExecution{TraceID: "candidate-trace", Usage: usage},
		JudgeTrace:  &TraceExecution{TraceID: "judge-trace", Usage: usage},
		ToolTrace:   []ToolTrace{{Name: "search_question_memory", Arguments: map[string]any{"api_key": "secret-token", "query": "private query"}, Error: "provider error containing private answer"}},
		HardChecks:  []HardCheck{{Name: "critical", Passed: true, Critical: true}},
		Score:       ScoreBreakdown{DeterministicScore: 60},
		PromptResolution: &PromptResolution{
			Requested:   PromptSelection{Key: "agent", Version: "v2", Source: "cozeloop"},
			Resolved:    PromptSelection{Key: "agent", Version: "v2", Source: "cozeloop"},
			ContentHash: "sha256:prompt-hash",
		},
		DurationMS: 123,
	}
	report := RunReport{
		RunID:          "run-1",
		CandidateModel: "candidate-model",
		JudgeModel:     "judge-model",
		Prompt:         &PromptSelection{Key: "agent", Version: "v2", Source: "cozeloop"},
		Cases:          []CaseResult{result},
	}

	items, err := BuildDatasetItems([]EvalCase{caseData}, report)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Identity != "run-1:case-1:1:1" || item.ItemKey == "" || item.ContentHash == "" {
		t.Fatalf("result identity = %+v", item)
	}
	if item.Fields["actual_output"] != "grounded answer" || item.Fields["candidate_trace_id"] != "candidate-trace" || item.Fields["judge_trace_id"] != "judge-trace" {
		t.Fatalf("mapped output/trace fields = %#v", item.Fields)
	}
	if item.Fields["required_facts"] != `["grounded"]` || item.Fields["forbidden_content"] != `["fabricated claim","API key"]` {
		t.Fatalf("mapped answer rules = required %q forbidden %q", item.Fields["required_facts"], item.Fields["forbidden_content"])
	}
	if item.Fields["candidate_total_tokens"] != "8" || item.Fields["judge_total_tokens"] != "8" || item.Fields["prompt_version"] != "v2" || item.Fields["prompt_resolved_version"] != "v2" || item.Fields["prompt_content_hash"] != "sha256:prompt-hash" {
		t.Fatalf("mapped usage/prompt fields = %#v", item.Fields)
	}
	if strings.Contains(item.Fields["tool_trace"], "private answer") || strings.Contains(item.Fields["tool_trace"], "secret-token") || strings.Contains(item.Fields["tool_trace"], "private query") {
		t.Fatalf("tool trace upload retained sensitive content: %s", item.Fields["tool_trace"])
	}
	if !strings.Contains(item.Fields["tool_trace"], `"arguments":{}`) {
		t.Fatalf("tool trace did not retain its sanitized arguments object: %s", item.Fields["tool_trace"])
	}

	result.RepeatIndex = 2
	second, err := BuildDatasetItems([]EvalCase{caseData}, RunReport{RunID: "run-1", Cases: []CaseResult{result}})
	if err != nil {
		t.Fatal(err)
	}
	if item.ItemKey == second[0].ItemKey || item.Identity == second[0].Identity {
		t.Fatal("different repeat index reused result identity")
	}
}

func TestDefaultEvaluationDatasetSchemaFitsPlatformColumnLimit(t *testing.T) {
	if got := len(DefaultEvaluationDatasetSpec().Fields); got > 50 {
		t.Fatalf("dataset schema has %d fields, exceeding the 50-column CozeLoop limit", got)
	}
}

func TestBuildDatasetItemsRejectsUnknownAndDuplicateResults(t *testing.T) {
	caseData := EvalCase{ID: "case-1", Version: DatasetVersion}
	unknown := CaseResult{RunID: "run-1", CaseID: "missing", CaseVersion: DatasetVersion, RepeatIndex: 1}
	if _, err := BuildDatasetItems([]EvalCase{caseData}, RunReport{RunID: "run-1", Cases: []CaseResult{unknown}}); err == nil {
		t.Fatal("BuildDatasetItems() error = nil, want unknown case error")
	}
	duplicate := CaseResult{RunID: "run-1", CaseID: "case-1", CaseVersion: DatasetVersion, RepeatIndex: 1}
	if _, err := BuildDatasetItems([]EvalCase{caseData}, RunReport{RunID: "run-1", Cases: []CaseResult{duplicate, duplicate}}); err == nil {
		t.Fatal("BuildDatasetItems() error = nil, want duplicate execution error")
	}
}

// Dataset synchronization tests.
type memoryEvaluationPlatform struct {
	dataset      Dataset
	draftItems   map[string]DatasetItem
	versions     []DatasetVersionRecord
	versionItems map[string][]DatasetItem
	writeFailure bool
}

func newMemoryEvaluationPlatform() *memoryEvaluationPlatform {
	return &memoryEvaluationPlatform{
		dataset:      Dataset{ID: "dataset-1", Key: EvaluationDatasetKey},
		draftItems:   make(map[string]DatasetItem),
		versionItems: make(map[string][]DatasetItem),
	}
}

func (p *memoryEvaluationPlatform) EnsureDataset(_ context.Context, _ DatasetSpec) (Dataset, error) {
	return p.dataset, nil
}

func (p *memoryEvaluationPlatform) ListDatasetVersions(_ context.Context, _ string) ([]DatasetVersionRecord, error) {
	return append([]DatasetVersionRecord(nil), p.versions...), nil
}

func (p *memoryEvaluationPlatform) ListDatasetItems(_ context.Context, _, versionID string) ([]DatasetItem, error) {
	if versionID != "" {
		return append([]DatasetItem(nil), p.versionItems[versionID]...), nil
	}
	items := make([]DatasetItem, 0, len(p.draftItems))
	for _, item := range p.draftItems {
		items = append(items, item)
	}
	return items, nil
}

func (p *memoryEvaluationPlatform) BatchCreateDatasetItems(_ context.Context, _ string, items []DatasetItem) ([]ItemWriteResult, error) {
	results := make([]ItemWriteResult, 0, len(items))
	for index, item := range items {
		p.draftItems[item.ItemKey] = item
		results = append(results, ItemWriteResult{ItemKey: item.ItemKey, IsNewItem: true})
		if p.writeFailure && index == 0 {
			p.writeFailure = false
			return nil, errors.New("simulated partial write")
		}
	}
	return results, nil
}

func (p *memoryEvaluationPlatform) CreateDatasetVersion(_ context.Context, _ string, spec DatasetVersionSpec) (DatasetVersionRecord, error) {
	id := fmt.Sprintf("version-%d", len(p.versions)+1)
	items := make([]DatasetItem, 0, len(p.draftItems))
	for _, item := range p.draftItems {
		items = append(items, item)
	}
	p.versionItems[id] = items
	version := DatasetVersionRecord{ID: id, Version: spec.Version}
	p.versions = append(p.versions, version)
	return version, nil
}

func testSyncPayload() DatasetSyncPayload {
	version, _ := EvaluationDatasetVersionForRun("run-1")
	return DatasetSyncPayload{
		RunID:   "run-1",
		Dataset: DefaultEvaluationDatasetSpec(),
		Version: DatasetVersionSpec{Version: version},
		Items: []DatasetItem{
			{ItemKey: "item-1", Identity: "run-1:case-1:1:1", ContentHash: "hash-1", Fields: map[string]string{"result_identity": "run-1:case-1:1:1"}},
			{ItemKey: "item-2", Identity: "run-1:case-2:1:1", ContentHash: "hash-2", Fields: map[string]string{"result_identity": "run-1:case-2:1:1"}},
		},
	}
}

func TestSyncEvaluationDatasetIsIdempotentForCommittedRun(t *testing.T) {
	client := newMemoryEvaluationPlatform()
	payload := testSyncPayload()

	first, err := SyncEvaluationDataset(context.Background(), client, payload)
	if err != nil {
		t.Fatal(err)
	}
	if first.UploadedItems != 2 || first.ReusedItems != 0 || first.Version.ID == "" {
		t.Fatalf("first sync result = %+v", first)
	}
	second, err := SyncEvaluationDataset(context.Background(), client, payload)
	if err != nil {
		t.Fatal(err)
	}
	if second.UploadedItems != 0 || second.ReusedItems != 2 || second.Version.ID != first.Version.ID {
		t.Fatalf("repeat sync result = %+v", second)
	}
	if len(client.versions) != 1 || len(client.draftItems) != 2 {
		t.Fatalf("platform state = versions %d, draft items %d", len(client.versions), len(client.draftItems))
	}
}

func TestSyncEvaluationDatasetResumesAfterPartialItemWrite(t *testing.T) {
	client := newMemoryEvaluationPlatform()
	client.writeFailure = true
	payload := testSyncPayload()

	if _, err := SyncEvaluationDataset(context.Background(), client, payload); err == nil {
		t.Fatal("first sync error = nil, want simulated write failure")
	}
	if len(client.versions) != 0 || len(client.draftItems) != 1 {
		t.Fatalf("partial platform state = versions %d, draft items %d", len(client.versions), len(client.draftItems))
	}
	result, err := SyncEvaluationDataset(context.Background(), client, payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.UploadedItems != 1 || result.ReusedItems != 1 || len(client.versions) != 1 {
		t.Fatalf("resumed sync result/state = %+v, versions %d", result, len(client.versions))
	}
}

func TestEvaluationDatasetVersionForRunUsesSemVerBuildMetadata(t *testing.T) {
	version, err := EvaluationDatasetVersionForRun("f22824de-0645-4de8-9dad-c993bcbde2de")
	if err != nil {
		t.Fatal(err)
	}
	if version != "0.0.0+run.f22824de-0645-4de8-9dad-c993bcbde2de" {
		t.Fatalf("evaluation dataset version = %q", version)
	}
	if _, err := EvaluationDatasetVersionForRun("invalid/run-id"); err == nil {
		t.Fatal("invalid run ID was accepted")
	}
	legacyVersion, err := EvaluationDatasetVersionForRun("legacy_run_id")
	if err != nil {
		t.Fatal(err)
	}
	legacyVersionAgain, err := EvaluationDatasetVersionForRun("legacy_run_id")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacyVersion, evaluationDatasetVersionPrefix) || len(legacyVersion) != maxEvaluationDatasetVersionLen || strings.Contains(legacyVersion, "legacy_run_id") || legacyVersionAgain != legacyVersion {
		t.Fatalf("legacy run ID should map to a deterministic bounded SemVer identifier: %q", legacyVersion)
	}
	longRunID := strings.Repeat("x", 100)
	longVersion, err := EvaluationDatasetVersionForRun(longRunID)
	if err != nil || len(longVersion) != maxEvaluationDatasetVersionLen {
		t.Fatalf("long legacy run version = %q, error = %v", longVersion, err)
	}
}

func TestSyncEvaluationDatasetRejectsMismatchedRunIdentity(t *testing.T) {
	payload := testSyncPayload()
	payload.RunID = "another-run"
	if _, err := SyncEvaluationDataset(context.Background(), newMemoryEvaluationPlatform(), payload); err == nil {
		t.Fatal("mismatched run ID was accepted")
	}
}

func TestSyncEvaluationDatasetReusesLegacyCommittedVersionDuringMigration(t *testing.T) {
	client := newMemoryEvaluationPlatform()
	payload := testSyncPayload()
	legacy := DatasetVersionRecord{ID: "legacy-version", Version: "run-run-1"}
	client.versions = append(client.versions, legacy)
	client.versionItems[legacy.ID] = append([]DatasetItem(nil), payload.Items...)
	payload.LegacyVersion = legacy.Version

	result, err := SyncEvaluationDataset(context.Background(), client, payload)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version.ID != legacy.ID || len(client.versions) != 1 {
		t.Fatalf("legacy committed version was not reused: result=%+v versions=%+v", result, client.versions)
	}
}

func TestSyncEvaluationDatasetRejectsIdentityReuseWithDifferentPayload(t *testing.T) {
	client := newMemoryEvaluationPlatform()
	payload := testSyncPayload()
	if _, err := SyncEvaluationDataset(context.Background(), client, payload); err != nil {
		t.Fatal(err)
	}
	payload.Items[0].Fields["actual_output"] = "changed"
	payload.Items[0].ContentHash = "different-hash"
	if _, err := SyncEvaluationDataset(context.Background(), client, payload); err == nil {
		t.Fatal("conflicting content sync error = nil, want conflict")
	}
}
