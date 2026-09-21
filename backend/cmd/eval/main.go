package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	"interview-memory-agent/backend/internal/eval"
	agentopenai "interview-memory-agent/backend/internal/infrastructure/agent/openai"
	"interview-memory-agent/backend/internal/infrastructure/config"
	cozeloopinfra "interview-memory-agent/backend/internal/infrastructure/cozeloop"
)

func main() {
	mode := flag.String("mode", "validate", "validate, validate-playground or live")
	dataset := flag.String("dataset", "../evals/interview-agent.v1.jsonl", "JSONL dataset path")
	playground := flag.String("playground", "../evals/interview-agent-playground.v1.json", "Prompt Playground manifest path")
	caseID := flag.String("case", "", "run only one case")
	tag := flag.String("tag", "", "run cases containing this tag")
	repeat := flag.Int("repeat", 1, "number of runs per selected case")
	timeout := flag.Duration("timeout", 90*time.Second, "per-case timeout")
	output := flag.String("output", "../evals/reports", "report output directory")
	promptVersion := flag.String("prompt-version", "", "override the Prompt Hub version")
	promptLabel := flag.String("prompt-label", "", "override the Prompt Hub label")
	requirePromptVersion := flag.Bool("require-prompt-version", false, "require Prompt Hub to use a fixed version")
	flag.Parse()

	allCases, err := eval.LoadCasesFile(*dataset)
	if err != nil {
		fatal(err)
	}
	if *mode == "validate-playground" {
		manifest, manifestErr := eval.LoadPromptPlaygroundFile(*playground)
		if manifestErr != nil {
			fatal(manifestErr)
		}
		if manifestErr := manifest.ValidateAgainst(allCases); manifestErr != nil {
			fatal(manifestErr)
		}
		fmt.Printf("validated Prompt Playground manifest %q against %d cases\n", manifest.PromptKey, len(allCases))
		return
	}
	cases := selectCases(allCases, *caseID, *tag)
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
	if strings.TrimSpace(*promptVersion) != "" && strings.TrimSpace(*promptLabel) != "" {
		fatal(errors.New("--prompt-version and --prompt-label cannot be used together"))
	}
	if strings.TrimSpace(*promptVersion) != "" {
		cfg.CozeLoop.PromptVersion = strings.TrimSpace(*promptVersion)
		cfg.CozeLoop.PromptLabel = ""
	}
	if strings.TrimSpace(*promptLabel) != "" {
		cfg.CozeLoop.PromptLabel = strings.TrimSpace(*promptLabel)
		cfg.CozeLoop.PromptVersion = ""
	}
	if strings.TrimSpace(cfg.CozeLoop.PromptVersion) != "" {
		cfg.CozeLoop.PromptLabel = ""
	}
	if (strings.TrimSpace(*promptVersion) != "" || strings.TrimSpace(*promptLabel) != "") && !cfg.CozeLoop.PromptEnabled {
		fatal(errors.New("Prompt version or label overrides require COZELOOP_PROMPT_ENABLED=true"))
	}
	if *requirePromptVersion {
		if !cfg.CozeLoop.PromptEnabled {
			fatal(errors.New("--require-prompt-version requires COZELOOP_PROMPT_ENABLED=true"))
		}
		version := strings.TrimSpace(cfg.CozeLoop.PromptVersion)
		if version == "" || strings.EqualFold(version, "latest") {
			fatal(errors.New("a concrete AGENT_PROMPT_VERSION or --prompt-version is required"))
		}
	}
	cozeLoopClient, err := cozeloopinfra.New(cfg.CozeLoop)
	if err != nil {
		fatal(fmt.Errorf("configure CozeLoop: %w", err))
	}
	if err := cozeLoopClient.RegisterEinoTracing(); err != nil {
		fatalWithCozeLoop(cozeLoopClient, fmt.Errorf("register CozeLoop tracing: %w", err))
	}
	defer closeCozeLoop(cozeLoopClient)
	promptProvider := agentprompt.Provider(agentprompt.NewLocalPromptProvider())
	if cfg.CozeLoop.PromptEnabled {
		if *requirePromptVersion {
			promptProvider, err = cozeloopinfra.NewStrictPromptProvider(cozeLoopClient, promptProvider)
		} else {
			promptProvider, err = cozeloopinfra.NewPromptProvider(cozeLoopClient, promptProvider)
		}
		if err != nil {
			fatalWithCozeLoop(cozeLoopClient, fmt.Errorf("configure prompt provider: %w", err))
		}
	}

	candidate, err := agentopenai.NewChatModel(context.Background(), cfg.OpenAI)
	if err != nil {
		fatalWithCozeLoop(cozeLoopClient, fmt.Errorf("candidate model: %w", err))
	}
	var judge eval.Judge
	if key, modelName := strings.TrimSpace(os.Getenv("EVAL_JUDGE_API_KEY")), strings.TrimSpace(os.Getenv("EVAL_JUDGE_MODEL")); key != "" && modelName != "" {
		judgeModel, judgeErr := agentopenai.NewChatModel(context.Background(), config.OpenAIConfig{APIKey: key, BaseURL: os.Getenv("EVAL_JUDGE_BASE_URL"), Model: modelName})
		if judgeErr != nil {
			fatalWithCozeLoop(cozeLoopClient, fmt.Errorf("judge model: %w", judgeErr))
		}
		judge, err = eval.NewLLMJudge(judgeModel)
		if err != nil {
			fatalWithCozeLoop(cozeLoopClient, err)
		}
	} else {
		fmt.Fprintln(os.Stderr, "warning: EVAL_JUDGE_API_KEY and EVAL_JUDGE_MODEL are not both configured; results will be unscored")
	}

	runner := &eval.Runner{Candidate: candidate, Judge: judge, PromptProvider: promptProvider}
	results := make([]eval.CaseResult, 0, len(cases)*(*repeat))
	for _, c := range cases {
		for i := 0; i < *repeat; i++ {
			results = append(results, runner.RunCase(context.Background(), c, *timeout))
		}
	}
	report := eval.BuildReport("live", results)
	promptSource := "local"
	if cfg.CozeLoop.PromptEnabled {
		promptSource = "cozeloop"
	}
	report.Prompt = &eval.PromptSelection{
		Key:     cfg.CozeLoop.PromptKey,
		Version: cfg.CozeLoop.PromptVersion,
		Label:   cfg.CozeLoop.PromptLabel,
		Source:  promptSource,
	}
	if err := eval.WriteReport(*output, report); err != nil {
		fatalWithCozeLoop(cozeLoopClient, err)
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
func closeCozeLoop(client *cozeloopinfra.Client) {
	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Close(closeContext); err != nil {
		fmt.Fprintln(os.Stderr, "eval: close CozeLoop:", err)
	}
}

func fatalWithCozeLoop(client *cozeloopinfra.Client, err error) {
	closeCozeLoop(client)
	fatal(err)
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "eval:", err); os.Exit(1) }
