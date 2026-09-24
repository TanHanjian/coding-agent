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
	message := schema.AssistantMessage("先澄清题意，再给出解法。", nil)
	message.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 13, CompletionTokens: 9, TotalTokens: 22}}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (plainCandidate) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return plainCandidate{}, nil
}

func TestRunnerExecutesProductionBuilderWithoutStorage(t *testing.T) {
	runner := &Runner{Candidate: plainCandidate{}}
	caseData := EvalCase{ID: "runner", Version: DatasetVersion, Input: EvalInput{Query: "说明算法题步骤"}, ToolExpectations: ToolExpectations{Forbidden: []string{"search_question_memory", "get_question_context"}}, AnswerExpectations: AnswerExpectations{MustContain: []string{"澄清题意"}}}
	result := runner.RunCaseWithMetadata(context.Background(), caseData, 5*time.Second, RunMetadata{
		RunID:       "run-123",
		CaseID:      caseData.ID,
		CaseVersion: DatasetVersion,
		RepeatIndex: 2,
		CaseTags:    []string{"smoke"},
	})
	if result.Error != "" {
		t.Fatalf("run error = %v", result.Error)
	}
	if result.Answer == "" {
		t.Fatal("answer is empty")
	}
	if result.Status != "unscored" {
		t.Fatalf("status = %q, want unscored without judge", result.Status)
	}
	if result.RunID != "run-123" || result.CaseVersion != DatasetVersion || result.RepeatIndex != 2 {
		t.Fatalf("run identity = %+v", result)
	}
	if result.Candidate.Usage == nil || *result.Candidate.Usage != (TokenUsage{PromptTokens: 13, CompletionTokens: 9, TotalTokens: 22}) {
		t.Fatalf("candidate usage = %+v", result.Candidate.Usage)
	}
	if result.JudgeTrace != nil {
		t.Fatalf("JudgeTrace = %+v, want nil without a judge", result.JudgeTrace)
	}
	if result.InfrastructureFailure {
		t.Fatal("missing optional Judge should not classify a successful Candidate run as an infrastructure failure")
	}
	if result.PromptResolution == nil || result.PromptResolution.Resolved.Source != "local" || result.PromptResolution.ContentHash == "" {
		t.Fatalf("prompt resolution = %+v", result.PromptResolution)
	}
}
