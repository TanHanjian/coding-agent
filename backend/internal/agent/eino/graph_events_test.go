package eino

import "testing"

func TestGenerationStepTrackerOrdersAndFinishesSteps(t *testing.T) {
	steps := newGenerationStepTracker()
	if got := steps.Current(); got != "" {
		t.Fatalf("initial step = %q, want empty", got)
	}
	if got := steps.Begin(); got != "step-1" {
		t.Fatalf("first step = %q, want step-1", got)
	}
	if got := steps.Finish(); got != "step-1" {
		t.Fatalf("finished step = %q, want step-1", got)
	}
	if got := steps.Finish(); got != "" {
		t.Fatalf("duplicate finish = %q, want empty", got)
	}
	if got := steps.Begin(); got != "step-2" {
		t.Fatalf("second step = %q, want step-2", got)
	}
}

func TestToolPresentationSummariesDoNotExposePayloads(t *testing.T) {
	input := toolInputSummary("search_question_memory")
	output := toolOutputSummary("search_question_memory")
	for name, summary := range map[string]map[string]string{"input": input, "output": output} {
		if summary["tool"] != "search_question_memory" {
			t.Fatalf("%s summary tool = %q", name, summary["tool"])
		}
		if len(summary) != 2 {
			t.Fatalf("%s summary = %#v, want only status and tool", name, summary)
		}
	}
}
