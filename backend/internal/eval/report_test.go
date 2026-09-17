package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReportDoesNotWriteSensitiveRuntimeData(t *testing.T) {
	total := 82.5
	dir := t.TempDir()
	report := BuildReport("live", []CaseResult{{CaseID: "a", Status: "passed", Score: ScoreBreakdown{Total: &total}}})
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
	if report.Summary.Passed != 1 || report.Summary.Average != 82.5 {
		t.Fatalf("summary = %#v", report.Summary)
	}
}
