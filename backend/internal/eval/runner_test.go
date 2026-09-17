package eval

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type plainCandidate struct{}

func (plainCandidate) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("先澄清题意，再给出解法。", nil), nil
}

func (plainCandidate) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("先澄清题意，再给出解法。", nil)}), nil
}

func (plainCandidate) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return plainCandidate{}, nil
}

func TestRunnerExecutesProductionBuilderWithoutStorage(t *testing.T) {
	runner := &Runner{Candidate: plainCandidate{}}
	caseData := EvalCase{ID: "runner", Version: DatasetVersion, Input: EvalInput{Query: "说明算法题步骤"}, ToolExpectations: ToolExpectations{Forbidden: []string{"search_question_memory", "get_question_context"}}, AnswerExpectations: AnswerExpectations{MustContain: []string{"澄清题意"}}}
	result := runner.RunCase(context.Background(), caseData, 5*time.Second)
	if result.Error != "" {
		t.Fatalf("run error = %v", result.Error)
	}
	if result.Answer == "" {
		t.Fatal("answer is empty")
	}
	if result.Status != "unscored" {
		t.Fatalf("status = %q, want unscored without judge", result.Status)
	}
}
