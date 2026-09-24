package eval

import "testing"

func TestScoreDeterministicPassesExpectedToolChain(t *testing.T) {
	c := EvalCase{ID: "case", Version: DatasetVersion, Input: EvalInput{Query: "q"}, ToolExpectations: ToolExpectations{
		Required:  []string{"search_question_memory", "get_question_context"},
		Ordered:   []string{"search_question_memory", "get_question_context"},
		Arguments: map[string]map[string]any{"get_question_context": {"questionId": "q-1"}},
	}, AnswerExpectations: AnswerExpectations{MustContain: []string{"事实"}}}
	b, checks := ScoreDeterministic(c, "这是事实。", []ToolTrace{
		{Name: "search_question_memory", Arguments: map[string]any{"query": "q"}},
		{Name: "get_question_context", Arguments: map[string]any{"questionId": "q-1"}},
	})
	if HasCriticalFailure(checks) {
		t.Fatalf("unexpected critical failure: %#v", checks)
	}
	if b.DeterministicScore != 60 {
		t.Fatalf("deterministic score = %v, want 60", b.DeterministicScore)
	}
	judge := JudgeResult{Scores: JudgeScores{Groundedness: 4, InstructionFollowing: 4, Completeness: 4, Actionability: 4, Rationale: "good"}}
	b = ApplyJudgeScore(b, judge)
	if b.Total == nil || *b.Total != 100 {
		t.Fatalf("total = %v, want 100", b.Total)
	}
}

func TestScoreDeterministicDetectsCriticalToolFailure(t *testing.T) {
	c := EvalCase{ID: "case", Version: DatasetVersion, Input: EvalInput{Query: "q"}, ToolExpectations: ToolExpectations{Required: []string{"search_question_memory"}, Forbidden: []string{"get_question_context"}}}
	b, checks := ScoreDeterministic(c, "回答", []ToolTrace{{Name: "get_question_context"}})
	if !HasCriticalFailure(checks) {
		t.Fatal("expected critical failure")
	}
	if b.DeterministicScore >= 60 {
		t.Fatalf("score = %v, want less than 60", b.DeterministicScore)
	}
}

func TestScoreDeterministicChecksForbiddenAnswer(t *testing.T) {
	c := EvalCase{ID: "case", Version: DatasetVersion, Input: EvalInput{Query: "q"}, AnswerExpectations: AnswerExpectations{CriticalForbid: []string{"API key"}}}
	_, checks := ScoreDeterministic(c, "这里泄露 API key", nil)
	if !HasCriticalFailure(checks) {
		t.Fatal("expected critical forbidden-content failure")
	}
}

func TestScoreDeterministicRejectsEmptyAnswer(t *testing.T) {
	c := EvalCase{ID: "case", Version: DatasetVersion, Input: EvalInput{Query: "q"}}
	_, checks := ScoreDeterministic(c, " \n\t", nil)
	if !HasCriticalFailure(checks) {
		t.Fatal("expected empty answer to be a critical failure")
	}
	if check := findHardCheck(checks, "answer_nonempty"); check == nil || check.Passed {
		t.Fatalf("answer_nonempty check = %#v, want failed check", check)
	}
}

func findHardCheck(checks []HardCheck, name string) *HardCheck {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}
