package eval

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type judgeModel struct{ calls int }

func (m *judgeModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.calls++
	if m.calls == 1 {
		return schema.AssistantMessage("not-json", nil), nil
	}
	return schema.AssistantMessage(`{"scores":{"groundedness":4,"instructionFollowing":3,"completeness":2,"actionability":1,"rationale":"需要补充行动步骤"}}`, nil), nil
}
func (m *judgeModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("{}", nil)}), nil
}

func TestLLMJudgeRetriesMalformedJSON(t *testing.T) {
	model := &judgeModel{}
	judge, err := NewLLMJudge(model)
	if err != nil {
		t.Fatal(err)
	}
	result, err := judge.Evaluate(context.Background(), JudgeInput{CaseID: "case", Query: "q", Answer: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || result.Scores.Groundedness != 4 || result.Scores.Actionability != 1 {
		t.Fatalf("calls=%d result=%#v", model.calls, result)
	}
}
