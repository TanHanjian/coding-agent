// Package eval contains the offline and live evaluation harness for the
// interview review agent. It deliberately sits outside the HTTP and storage
// layers so an evaluation cannot mutate a user's local database.
package eval

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/domain/answer"
	"interview-memory-agent/backend/internal/domain/question"
	"interview-memory-agent/backend/internal/domain/review"
)

const DatasetVersion = 1

type EvalCase struct {
	ID                 string             `json:"id"`
	Version            int                `json:"version"`
	Tags               []string           `json:"tags"`
	Input              EvalInput          `json:"input"`
	Fixtures           Fixtures           `json:"fixtures"`
	ToolExpectations   ToolExpectations   `json:"toolExpectations"`
	AnswerExpectations AnswerExpectations `json:"answerExpectations"`
}

type EvalInput struct {
	Query            string        `json:"query"`
	InterviewContext string        `json:"interviewContext,omitempty"`
	History          []EvalMessage `json:"history,omitempty"`
}

type EvalMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Fixtures struct {
	Questions    []QuestionFixture `json:"questions,omitempty"`
	SearchError  string            `json:"searchError,omitempty"`
	ContextError map[string]string `json:"contextError,omitempty"`
}

type QuestionFixture struct {
	Question    question.QuestionRecord `json:"question"`
	Answers     []answer.Attempt        `json:"answers,omitempty"`
	Reviews     []review.Record         `json:"reviews,omitempty"`
	Attachments []question.Attachment   `json:"attachments,omitempty"`
}

type ToolExpectations struct {
	Required  []string                  `json:"required,omitempty"`
	Forbidden []string                  `json:"forbidden,omitempty"`
	Ordered   []string                  `json:"ordered,omitempty"`
	MaxCalls  map[string]int            `json:"maxCalls,omitempty"`
	Arguments map[string]map[string]any `json:"arguments,omitempty"`
}

type AnswerExpectations struct {
	MustContain    []string `json:"mustContain,omitempty"`
	MustNotContain []string `json:"mustNotContain,omitempty"`
	CriticalForbid []string `json:"criticalForbid,omitempty"`
	JudgeRubric    string   `json:"judgeRubric,omitempty"`
}

type ToolTrace struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Error     string         `json:"error,omitempty"`
}

type HardCheck struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Points   int    `json:"points"`
	Reason   string `json:"reason,omitempty"`
	Critical bool   `json:"critical,omitempty"`
}

type JudgeScores struct {
	Groundedness         float64 `json:"groundedness"`
	InstructionFollowing float64 `json:"instructionFollowing"`
	Completeness         float64 `json:"completeness"`
	Actionability        float64 `json:"actionability"`
	Rationale            string  `json:"rationale"`
}

type JudgeResult struct {
	Scores JudgeScores `json:"scores"`
}

type ScoreBreakdown struct {
	ToolSelection        float64  `json:"toolSelection"`
	ToolArguments        float64  `json:"toolArguments"`
	ToolSequence         float64  `json:"toolSequence"`
	RequiredFacts        float64  `json:"requiredFacts"`
	ForbiddenContent     float64  `json:"forbiddenContent"`
	Groundedness         *float64 `json:"groundedness,omitempty"`
	InstructionFollowing *float64 `json:"instructionFollowing,omitempty"`
	Completeness         *float64 `json:"completeness,omitempty"`
	Actionability        *float64 `json:"actionability,omitempty"`
	DeterministicScore   float64  `json:"deterministicScore"`
	Total                *float64 `json:"total,omitempty"`
}

type CaseResult struct {
	CaseID string   `json:"caseId"`
	Tags   []string `json:"tags,omitempty"`
	Status string   `json:"status"`
	// Answer is retained in memory for Judge evaluation but is intentionally
	// omitted from persisted reports; reports contain scores and reasons only.
	Answer     string         `json:"-"`
	ToolTrace  []ToolTrace    `json:"toolTrace,omitempty"`
	HardChecks []HardCheck    `json:"hardChecks"`
	Judge      *JudgeResult   `json:"judge,omitempty"`
	Score      ScoreBreakdown `json:"score"`
	Error      string         `json:"error,omitempty"`
	DurationMS int64          `json:"durationMs"`
}

type ReportSummary struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	Unscored int     `json:"unscored"`
	Scored   int     `json:"scored"`
	Average  float64 `json:"average"`
	Min      float64 `json:"min"`
}

type RunReport struct {
	Version     int           `json:"version"`
	Mode        string        `json:"mode"`
	GeneratedAt time.Time     `json:"generatedAt"`
	Summary     ReportSummary `json:"summary"`
	Cases       []CaseResult  `json:"cases"`
}

func (c EvalCase) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case id is required")
	}
	if c.Version != DatasetVersion {
		return fmt.Errorf("unsupported dataset version %d", c.Version)
	}
	if strings.TrimSpace(c.Input.Query) == "" {
		return fmt.Errorf("case %q query is required", c.ID)
	}
	return nil
}

func (c EvalCase) MarshalJSON() ([]byte, error) {
	type alias EvalCase
	return json.Marshal(alias(c))
}
