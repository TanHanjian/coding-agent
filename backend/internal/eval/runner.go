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
	runID, err := NewRunID()
	if err != nil {
		return CaseResult{CaseID: c.ID, CaseVersion: c.Version, RepeatIndex: 1, Tags: append([]string(nil), c.Tags...), Status: "error", InfrastructureFailure: true, Error: "run_id_generation_error"}
	}
	return r.RunCaseWithMetadata(parent, c, timeout, RunMetadata{
		RunID:       runID,
		CaseID:      c.ID,
		CaseVersion: c.Version,
		CaseTags:    c.Tags,
		RepeatIndex: 1,
	})
}

func (r *Runner) RunCaseWithMetadata(parent context.Context, c EvalCase, timeout time.Duration, metadata RunMetadata) CaseResult {
	started := time.Now()
	metadata, identityErr := normalizeRunMetadata(metadata, c)
	result := CaseResult{
		RunID:       metadata.RunID,
		CaseID:      c.ID,
		CaseVersion: c.Version,
		RepeatIndex: metadata.RepeatIndex,
		Tags:        append([]string(nil), c.Tags...),
		Status:      "failed",
	}
	if identityErr != nil {
		result.InfrastructureFailure = true
		result.Error = "invalid_run_identity"
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	if err := c.Validate(); err != nil {
		result.InfrastructureFailure = true
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	if r.Candidate == nil {
		result.InfrastructureFailure = true
		result.Error = "candidate model is required"
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx := parent
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}
	store := newFixtureStore(c.Fixtures)
	if err := store.Validate(); err != nil {
		result.InfrastructureFailure = true
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	tools, err := interview.NewTools(store.Dependencies())
	if err != nil {
		result.InfrastructureFailure = true
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	promptProvider := r.PromptProvider
	if promptProvider == nil {
		promptProvider = agentprompt.NewLocalPromptProvider()
	}
	promptProvider = promptAuditProvider{delegate: promptProvider}
	builder, err := interview.NewBuilderWithPromptProvider(r.Candidate, promptProvider, tools...)
	if err != nil {
		result.InfrastructureFailure = true
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	candidateCapture := NewTraceCapture(metadata.traceMetadata("candidate"))
	executor, err := eino.NewExecutor(builder, eino.WithCallbackHandlers(newCandidateUsageCallback()))
	if err != nil {
		result.InfrastructureFailure = true
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		return result
	}
	req := chat.Request{History: toConversationHistory(c.Input.History), InterviewContext: c.Input.InterviewContext, UserMessage: conversation.Message{ID: "eval-user-" + c.ID, Role: conversation.MessageRoleUser, Content: c.Input.Query, Status: conversation.MessageStatusCompleted}}
	sink := &captureSink{}
	candidateCtx := WithTraceCapture(ctx, candidateCapture)
	err = executor.Stream(candidateCtx, req, sink)
	result.Candidate = candidateCapture.Snapshot()
	result.PromptResolution = candidateCapture.PromptResolution()
	result.Answer = sink.text()
	result.ToolTrace = store.traces()
	if err != nil {
		result.InfrastructureFailure = true
		result.Error = sanitizeRunError(err)
		result.Status = statusForError(err)
	}
	result.Score, result.HardChecks = ScoreDeterministic(c, result.Answer, result.ToolTrace)
	hardFailure := HasHardFailure(result.HardChecks)
	if err == nil && hardFailure {
		result.Status = "failed"
	}
	if err == nil && r.Judge != nil {
		judgeCapture := NewTraceCapture(metadata.traceMetadata("judge"))
		judgeCtx := WithTraceCapture(ctx, judgeCapture)
		judgeResult, judgeErr := r.Judge.Evaluate(judgeCtx, JudgeInput{CaseID: c.ID, Query: c.Input.Query, InterviewContext: c.Input.InterviewContext, Answer: result.Answer, ToolTrace: result.ToolTrace, ExpectedFacts: c.AnswerExpectations.MustContain, Rubric: c.AnswerExpectations.JudgeRubric})
		judgeExecution := judgeCapture.Snapshot()
		result.JudgeTrace = &judgeExecution
		if judgeErr != nil {
			result.InfrastructureFailure = true
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

func normalizeRunMetadata(metadata RunMetadata, c EvalCase) (RunMetadata, error) {
	if metadata.RunID == "" {
		var err error
		metadata.RunID, err = NewRunID()
		if err != nil {
			return RunMetadata{}, err
		}
	}
	if metadata.CaseID == "" {
		metadata.CaseID = c.ID
	}
	if metadata.CaseID != c.ID {
		return RunMetadata{}, fmt.Errorf("run metadata case id does not match case")
	}
	if metadata.CaseVersion == 0 {
		metadata.CaseVersion = c.Version
	}
	if metadata.CaseVersion != c.Version {
		return RunMetadata{}, fmt.Errorf("run metadata case version does not match case")
	}
	if metadata.RepeatIndex == 0 {
		metadata.RepeatIndex = 1
	}
	if metadata.RepeatIndex < 1 {
		return RunMetadata{}, fmt.Errorf("repeat index must be at least 1")
	}
	metadata.CaseTags = append([]string(nil), c.Tags...)
	return metadata, nil
}

func (m RunMetadata) traceMetadata(role string) TraceMetadata {
	modelName := m.CandidateModel
	if role == "judge" {
		modelName = m.JudgeModel
	}
	return TraceMetadata{
		RunID:         m.RunID,
		CaseID:        m.CaseID,
		CaseVersion:   m.CaseVersion,
		CaseTags:      m.CaseTags,
		RepeatIndex:   m.RepeatIndex,
		Role:          role,
		GitCommit:     m.GitCommit,
		Model:         modelName,
		PromptKey:     m.PromptKey,
		PromptVersion: m.PromptVersion,
		PromptLabel:   m.PromptLabel,
		PromptSource:  m.PromptSource,
	}
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
