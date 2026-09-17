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
	if got := steps.BeginAnswering(); got != "step-2" {
		t.Fatalf("answering step = %q, want step-2", got)
	}
	if got := steps.BeginAnswering(); got != "" {
		t.Fatalf("duplicate answering step = %q, want empty", got)
	}
	if steps.HasForwarded() {
		t.Fatal("HasForwarded() = true before callback text")
	}
	steps.MarkForwarded()
	if !steps.HasForwarded() {
		t.Fatal("HasForwarded() = false after callback text")
	}
}

func TestToolPresentationSummariesDoNotExposePayloads(t *testing.T) {
	presentation := toolPresentationFor("search_question_memory")
	if presentation.title != "检索题库" {
		t.Fatalf("title = %q, want 检索题库", presentation.title)
	}
	if presentation.input["summary"] != "正在检索题库" || presentation.output["summary"] != "已获取题库资料" {
		t.Fatalf("unexpected search presentation: %#v", presentation)
	}
	for name, summary := range map[string]map[string]string{"input": presentation.input, "output": presentation.output} {
		if len(summary) != 1 || summary["summary"] == "" {
			t.Fatalf("%s summary = %#v, want one safe summary field", name, summary)
		}
	}
}
