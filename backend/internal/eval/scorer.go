package eval

import (
	"fmt"
	"reflect"
	"strings"
)

// ScoreDeterministic applies the 60-point non-LLM portion of the rubric:
// tool behavior is 40 points and answer hard assertions are 20 points.
func ScoreDeterministic(c EvalCase, answer string, trace []ToolTrace) (ScoreBreakdown, []HardCheck) {
	checks := make([]HardCheck, 0, 5)
	toolNames := make([]string, 0, len(trace))
	for _, call := range trace {
		toolNames = append(toolNames, call.Name)
	}

	selectionOK := true
	for _, name := range c.ToolExpectations.Required {
		if !contains(toolNames, name) {
			selectionOK = false
			checks = append(checks, HardCheck{Name: "required_tool:" + name, Passed: false, Points: 0, Reason: "required tool was not called", Critical: true})
		}
	}
	for _, name := range c.ToolExpectations.Forbidden {
		if contains(toolNames, name) {
			selectionOK = false
			checks = append(checks, HardCheck{Name: "forbidden_tool:" + name, Passed: false, Points: 0, Reason: "forbidden tool was called", Critical: true})
		}
	}
	if selectionOK {
		checks = append(checks, HardCheck{Name: "tool_selection", Passed: true, Points: 15})
	}

	argumentPoints := 15
	for name, expected := range c.ToolExpectations.Arguments {
		matched := false
		for _, call := range trace {
			if call.Name == name && argumentsMatch(call.Arguments, expected) && call.Error == "" {
				matched = true
				break
			}
		}
		if !matched {
			argumentPoints = 0
			checks = append(checks, HardCheck{Name: "tool_arguments:" + name, Passed: false, Points: 0, Reason: "expected argument subset did not match", Critical: true})
		}
	}
	if argumentPoints > 0 {
		checks = append(checks, HardCheck{Name: "tool_arguments", Passed: true, Points: argumentPoints})
	}

	sequenceOK := orderedSubsequence(toolNames, c.ToolExpectations.Ordered)
	for name, max := range c.ToolExpectations.MaxCalls {
		if count(toolNames, name) > max {
			sequenceOK = false
		}
	}
	if sequenceOK {
		checks = append(checks, HardCheck{Name: "tool_sequence", Passed: true, Points: 10})
	} else {
		checks = append(checks, HardCheck{Name: "tool_sequence", Passed: false, Points: 0, Reason: "tool order or maximum call count failed", Critical: true})
	}

	requiredPoints := 12
	for _, text := range c.AnswerExpectations.MustContain {
		if !strings.Contains(strings.ToLower(answer), strings.ToLower(text)) {
			requiredPoints = 0
			checks = append(checks, HardCheck{Name: "required_fact:" + text, Passed: false, Points: 0, Reason: "required fact is missing"})
		}
	}
	if requiredPoints > 0 {
		checks = append(checks, HardCheck{Name: "required_facts", Passed: true, Points: requiredPoints})
	}

	forbiddenOK := true
	for _, text := range append(append([]string{}, c.AnswerExpectations.MustNotContain...), c.AnswerExpectations.CriticalForbid...) {
		if strings.Contains(strings.ToLower(answer), strings.ToLower(text)) {
			forbiddenOK = false
			checks = append(checks, HardCheck{Name: "forbidden_content:" + text, Passed: false, Points: 0, Reason: "forbidden content is present", Critical: contains(c.AnswerExpectations.CriticalForbid, text)})
		}
	}
	if forbiddenOK {
		checks = append(checks, HardCheck{Name: "forbidden_content", Passed: true, Points: 8})
	}

	breakdown := ScoreBreakdown{ToolSelection: points(checks, "tool_selection"), ToolArguments: points(checks, "tool_arguments"), ToolSequence: points(checks, "tool_sequence"), RequiredFacts: points(checks, "required_facts"), ForbiddenContent: points(checks, "forbidden_content")}
	breakdown.DeterministicScore = breakdown.ToolSelection + breakdown.ToolArguments + breakdown.ToolSequence + breakdown.RequiredFacts + breakdown.ForbiddenContent
	return breakdown, checks
}

func ApplyJudgeScore(b ScoreBreakdown, judge JudgeResult) ScoreBreakdown {
	groundedness := clamp(judge.Scores.Groundedness) / 4 * 15
	instruction := clamp(judge.Scores.InstructionFollowing) / 4 * 10
	completeness := clamp(judge.Scores.Completeness) / 4 * 10
	actionability := clamp(judge.Scores.Actionability) / 4 * 5
	b.Groundedness, b.InstructionFollowing, b.Completeness, b.Actionability = &groundedness, &instruction, &completeness, &actionability
	total := b.DeterministicScore + groundedness + instruction + completeness + actionability
	b.Total = &total
	return b
}

func HasCriticalFailure(checks []HardCheck) bool {
	for _, c := range checks {
		if c.Critical && !c.Passed {
			return true
		}
	}
	return false
}

// HasHardFailure is true for any failed deterministic assertion. A Judge can
// provide quality detail, but it cannot turn a failed contract assertion into
// a passing case.
func HasHardFailure(checks []HardCheck) bool {
	for _, c := range checks {
		if !c.Passed {
			return true
		}
	}
	return false
}

func argumentsMatch(actual, expected map[string]any) bool {
	for key, want := range expected {
		have, ok := actual[key]
		if !ok {
			return false
		}
		if !reflect.DeepEqual(normalizeNumber(have), normalizeNumber(want)) && fmt.Sprint(have) != fmt.Sprint(want) {
			return false
		}
	}
	return true
}
func normalizeNumber(v any) any {
	if f, ok := v.(float64); ok && f == float64(int(f)) {
		return int(f)
	}
	return v
}
func orderedSubsequence(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	j := 0
	for _, v := range have {
		if v == want[j] {
			j++
			if j == len(want) {
				return true
			}
		}
	}
	return false
}
func points(checks []HardCheck, name string) float64 {
	for _, c := range checks {
		if c.Name == name {
			return float64(c.Points)
		}
	}
	return 0
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func count(values []string, want string) int {
	n := 0
	for _, v := range values {
		if v == want {
			n++
		}
	}
	return n
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 4 {
		return 4
	}
	return v
}
