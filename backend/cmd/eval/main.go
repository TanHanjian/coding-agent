package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"interview-memory-agent/backend/internal/eval"
	agentopenai "interview-memory-agent/backend/internal/infrastructure/agent/openai"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

func main() {
	mode := flag.String("mode", "validate", "validate or live")
	dataset := flag.String("dataset", "../evals/interview-agent.v1.jsonl", "JSONL dataset path")
	caseID := flag.String("case", "", "run only one case")
	tag := flag.String("tag", "", "run cases containing this tag")
	repeat := flag.Int("repeat", 1, "number of runs per selected case")
	timeout := flag.Duration("timeout", 90*time.Second, "per-case timeout")
	output := flag.String("output", "../evals/reports", "report output directory")
	flag.Parse()

	cases, err := eval.LoadCasesFile(*dataset)
	if err != nil {
		fatal(err)
	}
	cases = selectCases(cases, *caseID, *tag)
	if len(cases) == 0 {
		fatal(errors.New("no cases matched the requested filters"))
	}
	if *repeat < 1 {
		fatal(errors.New("repeat must be at least 1"))
	}
	if *mode == "validate" {
		fmt.Printf("validated %d evaluation cases\n", len(cases))
		return
	}
	if *mode != "live" {
		fatal(fmt.Errorf("unsupported mode %q", *mode))
	}

	// Keep evaluation configuration loading identical to the server. In
	// particular, config.Load reads backend/.env when the command is run from
	// the backend directory; reading os.Getenv directly would miss that file.
	cfg, err := config.Load()
	if err != nil {
		fatal(fmt.Errorf("load evaluation config: %w", err))
	}
	candidate, err := agentopenai.NewChatModel(context.Background(), cfg.OpenAI)
	if err != nil {
		fatal(fmt.Errorf("candidate model: %w", err))
	}
	var judge eval.Judge
	if key, modelName := strings.TrimSpace(os.Getenv("EVAL_JUDGE_API_KEY")), strings.TrimSpace(os.Getenv("EVAL_JUDGE_MODEL")); key != "" && modelName != "" {
		judgeModel, judgeErr := agentopenai.NewChatModel(context.Background(), config.OpenAIConfig{APIKey: key, BaseURL: os.Getenv("EVAL_JUDGE_BASE_URL"), Model: modelName})
		if judgeErr != nil {
			fatal(fmt.Errorf("judge model: %w", judgeErr))
		}
		judge, err = eval.NewLLMJudge(judgeModel)
		if err != nil {
			fatal(err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "warning: EVAL_JUDGE_API_KEY and EVAL_JUDGE_MODEL are not both configured; results will be unscored")
	}

	runner := &eval.Runner{Candidate: candidate, Judge: judge}
	results := make([]eval.CaseResult, 0, len(cases)*(*repeat))
	for _, c := range cases {
		for i := 0; i < *repeat; i++ {
			results = append(results, runner.RunCase(context.Background(), c, *timeout))
		}
	}
	report := eval.BuildReport("live", results)
	if err := eval.WriteReport(*output, report); err != nil {
		fatal(err)
	}
	fmt.Printf("evaluated %d runs: passed=%d failed=%d unscored=%d average=%.2f\n", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Unscored, report.Summary.Average)
}

func selectCases(cases []eval.EvalCase, caseID, tag string) []eval.EvalCase {
	out := make([]eval.EvalCase, 0, len(cases))
	for _, c := range cases {
		if caseID != "" && c.ID != caseID {
			continue
		}
		if tag != "" && !contains(c.Tags, tag) {
			continue
		}
		out = append(out, c)
	}
	return out
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "eval:", err); os.Exit(1) }
