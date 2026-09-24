package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReportDoesNotWriteSensitiveRuntimeData(t *testing.T) {
	total := 82.5
	dir := t.TempDir()
	report := BuildReport("live", []CaseResult{{
		RunID:       "run-123",
		CaseID:      "a",
		CaseVersion: DatasetVersion,
		RepeatIndex: 1,
		Status:      "passed",
		Candidate: TraceExecution{TraceID: "candidate-trace", Usage: &TokenUsage{
			PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
		}},
		JudgeTrace: &TraceExecution{TraceID: "judge-trace", Usage: &TokenUsage{
			PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6,
		}},
		PromptResolution: &PromptResolution{
			Requested:   PromptSelection{Key: "agent", Version: "1.0", Source: "cozeloop"},
			Resolved:    PromptSelection{Key: "agent", Version: "1.0", Source: "cozeloop"},
			ContentHash: "sha256:abc",
		},
		Score: ScoreBreakdown{Total: &total},
	}})
	report.RunID = "run-123"
	report.Prompt = &PromptSelection{Key: "agent", Version: "1.0", Source: "cozeloop"}
	if !report.Summary.ReleaseEligible {
		t.Fatalf("fully scored fixed-version report should be release eligible: %+v", report.Summary)
	}
	if err := WriteReport(dir, report); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "OPENAI_API_KEY") {
		t.Fatal("summary contains a secret name")
	}
	if !strings.Contains(string(data), "candidate-trace") || !strings.Contains(string(data), "judge-trace") || !strings.Contains(string(data), "10/5/15") {
		t.Fatalf("summary missing trace or usage metadata: %s", data)
	}
	if !strings.Contains(string(data), "Prompt resolution") || !strings.Contains(string(data), "sha256:abc") {
		t.Fatalf("summary missing prompt resolution audit: %s", data)
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted RunReport
	if err := json.Unmarshal(jsonData, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.RunID != "run-123" || persisted.Cases[0].Candidate.TraceID != "candidate-trace" || persisted.Cases[0].JudgeTrace.TraceID != "judge-trace" {
		t.Fatalf("persisted trace identities = %+v", persisted)
	}
	if report.Summary.Passed != 1 || report.Summary.Average != 82.5 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	report.CozeLoopSync = &CozeLoopSyncStatus{Status: "synced", DatasetVersion: "0.0.0+run.run-123"}
	if err := WriteReport(dir, report); err != nil {
		t.Fatalf("rewrite report atomically: %v", err)
	}
	loaded, err := LoadReport(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CozeLoopSync == nil || loaded.CozeLoopSync.Status != "synced" {
		t.Fatalf("loaded CozeLoop sync metadata = %+v", loaded.CozeLoopSync)
	}
}

func TestBuildReportCountsInfrastructureFailuresWithoutHidingSamples(t *testing.T) {
	total := 92.0
	report := BuildReport("live", []CaseResult{
		{
			CaseID: "quality-pass", Status: "passed",
			PromptResolution: &PromptResolution{Resolved: PromptSelection{Source: "local"}},
			Score:            ScoreBreakdown{Total: &total},
		},
		{CaseID: "infrastructure-error", Status: "error", InfrastructureFailure: true},
	})
	if report.Summary.Total != 2 || report.Summary.Valid != 1 || report.Summary.Invalid != 1 || report.Summary.Passed != 1 || report.Summary.Failed != 1 || report.Summary.ReleaseEligible {
		t.Fatalf("run summary hid an invalid sample or allowed release: %+v", report.Summary)
	}
	if !strings.Contains(renderSummary(report), "- Invalid: 1") || !strings.Contains(renderSummary(report), "Release eligible: `not eligible`") {
		t.Fatalf("summary markdown omitted release eligibility: %s", renderSummary(report))
	}
}

func TestBuildReportRejectsFallbackAndUnscoredRunsForRelease(t *testing.T) {
	total := 85.0
	prompt := &PromptResolution{
		Requested: PromptSelection{Key: "remote", Label: "production", Source: "cozeloop"},
		Resolved:  PromptSelection{Key: "local", Version: "embedded", Source: "local"},
		Fallback:  true,
	}
	report := BuildReport("live", []CaseResult{{Status: "passed", PromptResolution: prompt, Score: ScoreBreakdown{Total: &total}}})
	if report.Summary.ReleaseEligible {
		t.Fatal("Prompt fallback must invalidate release eligibility")
	}
	unscored := BuildReport("live", []CaseResult{{Status: "unscored", PromptResolution: &PromptResolution{Resolved: PromptSelection{Source: "local"}}}})
	if unscored.Summary.ReleaseEligible {
		t.Fatal("unscored runs must not be release eligible")
	}
}
