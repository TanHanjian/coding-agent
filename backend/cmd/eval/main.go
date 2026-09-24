package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	"interview-memory-agent/backend/internal/eval"
	agentopenai "interview-memory-agent/backend/internal/infrastructure/agent/openai"
	"interview-memory-agent/backend/internal/infrastructure/config"
	cozeloopinfra "interview-memory-agent/backend/internal/infrastructure/cozeloop"
)

// Evaluation command modes and orchestration.
func main() {
	mode := flag.String("mode", "validate", "validate, validate-playground, live or sync-pending")
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
	publishCozeLoop := flag.Bool("publish-cozeloop", false, "upload authorized evaluation content to the configured CozeLoop workspace")
	runIDFlag := flag.String("run-id", "", "run ID to resume when mode=sync-pending")
	flag.Parse()

	if *publishCozeLoop && *mode != "live" {
		fatal(errors.New("--publish-cozeloop can only be used with --mode=live"))
	}
	if *mode == "sync-pending" {
		if strings.TrimSpace(*runIDFlag) == "" {
			fatal(errors.New("--run-id is required when --mode=sync-pending"))
		}
		cfg, err := config.Load()
		if err != nil {
			fatal(fmt.Errorf("load evaluation config: %w", err))
		}
		platform, err := cozeloopinfra.NewEvaluationClient(cfg.CozeLoop)
		if err != nil {
			fatal(err)
		}
		reportDir := filepath.Join(*output, *runIDFlag)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := syncPendingDataset(ctx, cozeloopinfra.PrivatePendingDirectory(cfg.DataDir), reportDir, platform); err != nil {
			fatal(err)
		}
		fmt.Printf("synced pending CozeLoop dataset for run %s; report=%s\n", *runIDFlag, reportDir)
		return
	}

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
	var evaluationPlatform eval.EvaluationPlatform
	if *publishCozeLoop {
		evaluationPlatform, err = cozeloopinfra.NewEvaluationClient(cfg.CozeLoop)
		if err != nil {
			fatal(fmt.Errorf("configure CozeLoop evaluation: %w", err))
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
	runID, err := eval.NewRunID()
	if err != nil {
		fatalWithCozeLoop(cozeLoopClient, err)
	}
	promptSource := "local"
	promptKey := agentprompt.AgentPromptKey
	promptVersionSelection := ""
	promptLabelSelection := ""
	if cfg.CozeLoop.PromptEnabled {
		promptSource = "cozeloop"
		promptKey = cfg.CozeLoop.PromptKey
		promptVersionSelection = cfg.CozeLoop.PromptVersion
		promptLabelSelection = cfg.CozeLoop.PromptLabel
	}
	judgeModelName := ""
	if judge != nil {
		judgeModelName = strings.TrimSpace(os.Getenv("EVAL_JUDGE_MODEL"))
	}
	gitCommit := strings.TrimSpace(os.Getenv("GITHUB_SHA"))
	if gitCommit == "" {
		gitCommit = strings.TrimSpace(os.Getenv("GIT_COMMIT"))
	}
	results := make([]eval.CaseResult, 0, len(cases)*(*repeat))
	for _, c := range cases {
		for i := 0; i < *repeat; i++ {
			metadata := eval.RunMetadata{
				RunID:          runID,
				CaseID:         c.ID,
				CaseVersion:    c.Version,
				CaseTags:       c.Tags,
				RepeatIndex:    i + 1,
				GitCommit:      gitCommit,
				CandidateModel: cfg.OpenAI.Model,
				JudgeModel:     judgeModelName,
				PromptKey:      promptKey,
				PromptVersion:  promptVersionSelection,
				PromptLabel:    promptLabelSelection,
				PromptSource:   promptSource,
			}
			results = append(results, runner.RunCaseWithMetadata(context.Background(), c, *timeout, metadata))
		}
	}
	report := eval.BuildReport("live", results)
	report.RunID = runID
	report.GitCommit = gitCommit
	report.CandidateModel = cfg.OpenAI.Model
	report.JudgeModel = judgeModelName
	report.Prompt = &eval.PromptSelection{
		Key:     promptKey,
		Version: promptVersionSelection,
		Label:   promptLabelSelection,
		Source:  promptSource,
	}
	reportDir := *output
	if *publishCozeLoop {
		reportDir = filepath.Join(*output, runID)
	}
	if err := eval.WriteReport(reportDir, report); err != nil {
		fatalWithCozeLoop(cozeLoopClient, err)
	}
	if *publishCozeLoop {
		pendingDir := cozeloopinfra.PrivatePendingDirectory(cfg.DataDir)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		syncErr := prepareAndSyncDataset(ctx, pendingDir, reportDir, cases, report, evaluationPlatform)
		cancel()
		if syncErr != nil {
			saved, loadErr := eval.LoadReport(filepath.Join(reportDir, "report.json"))
			if loadErr == nil && (saved.CozeLoopSync == nil || saved.CozeLoopSync.Status != "synced") {
				pendingPath, pathErr := pendingPathForRun(pendingDir, runID)
				pending := pathErr == nil
				if pending {
					if _, statErr := os.Stat(pendingPath); statErr != nil {
						pending = false
					}
				}
				saved.CozeLoopSync = &eval.CozeLoopSyncStatus{
					Status:         "failed",
					ErrorCategory:  classifyPublishError(syncErr),
					DatasetKey:     eval.EvaluationDatasetKey,
					PendingPayload: pending,
				}
				_ = eval.WriteReport(reportDir, saved)
			}
			fmt.Fprintf(os.Stderr, "CozeLoop publish failed; local report retained at %s\n", reportDir)
			fatalWithCozeLoop(cozeLoopClient, syncErr)
		}
	}
	fmt.Printf("evaluated %d runs: passed=%d failed=%d unscored=%d average=%.2f report=%s\n", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Unscored, report.Summary.Average, reportDir)
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

// CozeLoop dataset preparation and pending-sync workflow.
func prepareAndSyncDataset(ctx context.Context, pendingDir, reportDir string, cases []eval.EvalCase, report eval.RunReport, platform eval.EvaluationPlatform) error {
	items, err := eval.BuildDatasetItems(cases, report)
	if err != nil {
		return err
	}
	version, err := eval.EvaluationDatasetVersionForRun(report.RunID)
	if err != nil {
		return err
	}
	payload := eval.DatasetSyncPayload{
		RunID:   report.RunID,
		Dataset: eval.DefaultEvaluationDatasetSpec(),
		Version: eval.DatasetVersionSpec{
			Version:     version,
			Description: "Local evaluation run " + report.RunID,
		},
		Items: items,
	}
	pending := cozeloopinfra.PendingDatasetSync{RunID: report.RunID, Payload: payload}
	if _, err := cozeloopinfra.SavePendingDatasetSync(pendingDir, pending); err != nil {
		return err
	}
	report.CozeLoopSync = &eval.CozeLoopSyncStatus{
		Status:         "pending",
		DatasetKey:     payload.Dataset.Key,
		PendingPayload: true,
	}
	if err := eval.WriteReport(reportDir, report); err != nil {
		return err
	}
	return syncPendingDataset(ctx, pendingDir, reportDir, platform)
}

func syncPendingDataset(ctx context.Context, pendingDir, reportDir string, platform eval.EvaluationPlatform) error {
	if platform == nil {
		return errors.New("CozeLoop evaluation platform client is required")
	}
	pendingPath, err := pendingPathForRun(pendingDir, filepath.Base(reportDir))
	if err != nil {
		return err
	}
	reportPath := filepath.Join(reportDir, "report.json")
	report, err := eval.LoadReport(reportPath)
	if err != nil {
		return err
	}
	if report.RunID != filepath.Base(reportDir) {
		return errors.New("report run ID does not match the requested pending sync")
	}
	if report.CozeLoopSync != nil && report.CozeLoopSync.Status == "synced" {
		if err := cozeloopinfra.DeletePendingDatasetSync(pendingPath); err != nil {
			return err
		}
		report.CozeLoopSync.PendingPayload = false
		return eval.WriteReport(reportDir, report)
	}
	pending, err := cozeloopinfra.ReadPendingDatasetSync(pendingPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("no pending CozeLoop dataset payload exists for this run")
		}
		return err
	}
	if pending.RunID != report.RunID {
		return errors.New("pending payload run ID does not match the local report")
	}
	report.CozeLoopSync = &eval.CozeLoopSyncStatus{
		Status:         "syncing",
		DatasetKey:     pending.Payload.Dataset.Key,
		PendingPayload: true,
	}
	if err := eval.WriteReport(reportDir, report); err != nil {
		return err
	}

	result, syncErr := eval.SyncEvaluationDataset(ctx, platform, pending.Payload)
	if syncErr != nil {
		report.CozeLoopSync.Status = "failed"
		report.CozeLoopSync.ErrorCategory = classifyPublishError(syncErr)
		report.CozeLoopSync.PendingPayload = true
		if isContentConsentRevoked(syncErr) {
			if deleteErr := cozeloopinfra.DeletePendingDatasetSync(pendingPath); deleteErr != nil {
				return errors.Join(syncErr, deleteErr)
			}
			report.CozeLoopSync.PendingPayload = false
		}
		if err := eval.WriteReport(reportDir, report); err != nil {
			return errors.Join(syncErr, err)
		}
		return syncErr
	}
	report.CozeLoopSync = &eval.CozeLoopSyncStatus{
		Status:           "synced",
		DatasetKey:       result.Dataset.Key,
		DatasetID:        result.Dataset.ID,
		DatasetVersion:   result.Version.Version,
		DatasetVersionID: result.Version.ID,
		UploadedItems:    result.UploadedItems,
		ReusedItems:      result.ReusedItems,
		PendingPayload:   true,
		SyncedAt:         time.Now().UTC(),
	}
	if err := eval.WriteReport(reportDir, report); err != nil {
		return err
	}
	if err := cozeloopinfra.DeletePendingDatasetSync(pendingPath); err != nil {
		return err
	}
	report.CozeLoopSync.PendingPayload = false
	return eval.WriteReport(reportDir, report)
}

func pendingPathForRun(pendingDir, runID string) (string, error) {
	return cozeloopinfra.PendingDatasetSyncPath(pendingDir, runID)
}

func isContentConsentRevoked(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "content authorization is no longer active") || strings.Contains(message, "content authorization does not match")
}

func classifyPublishError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case isContentConsentRevoked(err):
		return "authorization"
	case strings.Contains(message, "authentication failed"):
		return "authentication"
	case strings.Contains(message, "permission denied"):
		return "permission"
	case strings.Contains(message, "timeout"):
		return "timeout"
	case strings.Contains(message, "conflicts with this run"), strings.Contains(message, "different content"):
		return "identity_conflict"
	case strings.Contains(message, "rejected"):
		return "api_rejected"
	case strings.Contains(message, "transport_error"):
		return "transport"
	default:
		return "platform_or_local_error"
	}
}
