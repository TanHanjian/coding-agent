package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPromptPlaygroundManifestValidatesAgainstDataset(t *testing.T) {
	manifest := PromptPlaygroundManifest{
		Version:   PromptPlaygroundVersion,
		PromptKey: "interview-review-agent",
		Variables: []PromptVariableSpec{
			{Name: "history", Type: "placeholder", Required: true},
			{Name: "query", Type: "string", Required: true},
			{Name: "interview_context", Type: "string", Required: true},
			{Name: "conversation_summary", Type: "string", Required: true},
		},
		Scenarios: []PromptPlaygroundScenario{
			{ID: "direct", Purpose: "direct answer", CaseIDs: []string{"one"}},
			{ID: "search", Purpose: "search", CaseIDs: []string{"two"}},
		},
	}
	cases := []EvalCase{
		{ID: "one", Version: DatasetVersion, Input: EvalInput{Query: "one"}},
		{ID: "two", Version: DatasetVersion, Input: EvalInput{Query: "two"}},
	}
	if err := manifest.ValidateAgainst(cases); err != nil {
		t.Fatalf("ValidateAgainst() error = %v", err)
	}
}

func TestLoadPromptPlaygroundFileRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"promptKey":"interview-review-agent","variables":[],"scenarios":[],"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPromptPlaygroundFile(path); err == nil {
		t.Fatal("LoadPromptPlaygroundFile() error = nil, want unknown field error")
	}
}

func TestPromptPlaygroundManifestRejectsUnmappedCase(t *testing.T) {
	manifest := validPromptPlaygroundManifest()
	manifest.Scenarios[0].CaseIDs = nil
	cases := []EvalCase{{ID: "one", Version: DatasetVersion, Input: EvalInput{Query: "one"}}}
	if err := manifest.ValidateAgainst(cases); err == nil {
		t.Fatal("ValidateAgainst() error = nil, want unmapped case error")
	}
}

func validPromptPlaygroundManifest() PromptPlaygroundManifest {
	return PromptPlaygroundManifest{
		Version:   PromptPlaygroundVersion,
		PromptKey: "interview-review-agent",
		Variables: []PromptVariableSpec{
			{Name: "history", Type: "placeholder", Required: true},
			{Name: "query", Type: "string", Required: true},
			{Name: "interview_context", Type: "string", Required: true},
			{Name: "conversation_summary", Type: "string", Required: true},
		},
		Scenarios: []PromptPlaygroundScenario{{ID: "direct", Purpose: "direct answer", CaseIDs: []string{"one"}}},
	}
}
