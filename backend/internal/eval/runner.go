package eval

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"interview-memory-agent/backend/internal/agent/eino"
	"interview-memory-agent/backend/internal/agent/interview"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

type Judge interface {
	Evaluate(context.Context, JudgeInput) (JudgeResult, error)
}

type JudgeInput struct {
	CaseID           string
	Query            string
	InterviewContext string
	Answer           string
	ToolTrace        []ToolTrace
	ExpectedFacts    []string
	Rubric           string
}

type Runner struct {
	Candidate      model.ToolCallingChatModel
	Judge          Judge
	PromptProvider agentprompt.Provider
}

func (r *Runner) RunCase(parent context.Context, c EvalCase, timeout time.Duration) CaseResult {
	started := time.Now()
	result := CaseResult{CaseID: c.ID, Tags: append([]string(nil), c.Tags...), Status: "failed"}
	if err := c.Validate(); err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	if r.Candidate == nil {
		result.Error = "candidate model is required"
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}
	store := newFixtureStore(c.Fixtures)
	if err := store.Validate(); err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	tools, err := interview.NewTools(store.Dependencies())
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	promptProvider := r.PromptProvider
	if promptProvider == nil {
		promptProvider = agentprompt.NewLocalPromptProvider()
	}
	builder, err := interview.NewBuilderWithPromptProvider(r.Candidate, promptProvider, tools...)
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	executor, err := eino.NewExecutor(builder)
	if err != nil {
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	req := chat.Request{History: toConversationHistory(c.Input.History), InterviewContext: c.Input.InterviewContext, UserMessage: conversation.Message{ID: "eval-user-" + c.ID, Role: conversation.MessageRoleUser, Content: c.Input.Query, Status: conversation.MessageStatusCompleted}}
	sink := &captureSink{}
	err = executor.Stream(ctx, req, sink)
	result.Answer = sink.text()
	result.ToolTrace = store.traces()
	if err != nil {
		result.Error = sanitizeRunError(err)
		result.Status = statusForError(err)
	}
	result.Score, result.HardChecks = ScoreDeterministic(c, result.Answer, result.ToolTrace)
	hardFailure := HasHardFailure(result.HardChecks)
	if err == nil && hardFailure {
		result.Status = "failed"
	}
	if err == nil && r.Judge != nil {
		judgeResult, judgeErr := r.Judge.Evaluate(ctx, JudgeInput{CaseID: c.ID, Query: c.Input.Query, InterviewContext: c.Input.InterviewContext, Answer: result.Answer, ToolTrace: result.ToolTrace, ExpectedFacts: c.AnswerExpectations.MustContain, Rubric: c.AnswerExpectations.JudgeRubric})
		if judgeErr != nil {
			result.Status = "unscored"
			result.Error = "judge: " + sanitizeRunError(judgeErr)
		} else {
			result.Judge = &judgeResult
			result.Score = ApplyJudgeScore(result.Score, judgeResult)
		}
	} else if err == nil && !hardFailure {
		result.Status = "unscored"
	}
	if err == nil && HasCriticalFailure(result.HardChecks) {
		result.Status = "failed"
	}
	if err == nil && hardFailure {
		result.Status = "failed"
	}
	if err == nil && !hardFailure && result.Status != "unscored" && result.Score.Total != nil {
		if *result.Score.Total >= 80 {
			result.Status = "passed"
		} else {
			result.Status = "failed"
		}
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result
}

func toConversationHistory(messages []EvalMessage) []conversation.Message {
	out := make([]conversation.Message, 0, len(messages))
	for i, m := range messages {
		role := conversation.MessageRoleUser
		if strings.EqualFold(m.Role, "assistant") {
			role = conversation.MessageRoleAssistant
		}
		out = append(out, conversation.Message{ID: fmt.Sprintf("eval-history-%d", i), Role: role, Content: m.Content, Status: conversation.MessageStatusCompleted})
	}
	return out
}
func sanitizeRunError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "deadline exceeded"):
		return "timeout"
	case strings.Contains(message, "canceled") || strings.Contains(message, "cancelled"):
		return "cancelled"
	case strings.Contains(message, "judge") || strings.Contains(message, "json"):
		return "judge_error"
	case strings.Contains(message, "tool"):
		return "tool_error"
	case strings.Contains(message, "401") || strings.Contains(message, "403") || strings.Contains(message, "api key"):
		return "model_auth_error"
	default:
		return "execution_error"
	}
}
func statusForError(err error) string {
	if errorsIsContext(err) {
		return "timeout"
	}
	return "error"
}
func errorsIsContext(err error) bool {
	return strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "context canceled")
}

type captureSink struct{ chunks []string }

func (s *captureSink) WriteChunk(_ context.Context, chunk string) error {
	s.chunks = append(s.chunks, chunk)
	return nil
}
func (s *captureSink) WriteEvent(_ context.Context, _ chat.GenerationEvent) error { return nil }
func (s *captureSink) text() string                                               { return strings.Join(s.chunks, "") }
