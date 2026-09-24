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
	content := "not-json"
	if m.calls > 1 {
		content = `{"scores":{"groundedness":4,"instructionFollowing":3,"completeness":2,"actionability":1,"rationale":"需要补充行动步骤"}}`
	}
	message := schema.AssistantMessage(content, nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 4, CompletionTokens: 3, TotalTokens: 7}}
	return message, nil
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
	capture := NewTraceCapture(TraceMetadata{RunID: "run-1", CaseID: "case", Role: "judge"})
	result, err := judge.Evaluate(WithTraceCapture(context.Background(), capture), JudgeInput{CaseID: "case", Query: "q", Answer: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 || result.Scores.Groundedness != 4 || result.Scores.Actionability != 1 {
		t.Fatalf("calls=%d result=%#v", model.calls, result)
	}
	if got := capture.Snapshot().Usage; got == nil || *got != (TokenUsage{PromptTokens: 8, CompletionTokens: 6, TotalTokens: 14}) {
		t.Fatalf("Judge usage = %+v, want both attempts included", got)
	}
}
