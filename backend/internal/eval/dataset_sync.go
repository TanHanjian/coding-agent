package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Dataset schema and platform port.
type DatasetField struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type DatasetSpec struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Fields      []DatasetField `json:"fields"`
}

type Dataset struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

type DatasetItem struct {
	ItemKey     string            `json:"itemKey"`
	Identity    string            `json:"identity"`
	ContentHash string            `json:"contentHash"`
	Fields      map[string]string `json:"fields"`
}

type DatasetVersionSpec struct {
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type DatasetVersionRecord struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ItemWriteResult struct {
	ItemKey     string
	ItemID      string
	ItemVersion string
	IsNewItem   bool
}

// EvaluationPlatform is the application port for the small subset of CozeLoop
// evaluation APIs used by this repository. It intentionally exposes domain
// data instead of platform request DTOs.
type EvaluationPlatform interface {
	EnsureDataset(context.Context, DatasetSpec) (Dataset, error)
	ListDatasetVersions(context.Context, string) ([]DatasetVersionRecord, error)
	ListDatasetItems(context.Context, string, string) ([]DatasetItem, error)
	BatchCreateDatasetItems(context.Context, string, []DatasetItem) ([]ItemWriteResult, error)
	CreateDatasetVersion(context.Context, string, DatasetVersionSpec) (DatasetVersionRecord, error)
}

const EvaluationDatasetKey = "interview-agent-evaluation"

func DefaultEvaluationDatasetSpec() DatasetSpec {
	fields := []DatasetField{
		{Name: "case_id", Description: "Stable input case identity", Required: true},
		{Name: "case_version", Description: "Version of the local JSONL case", Required: true},
		{Name: "tags", Description: "Evaluation case tags"},
		{Name: "run_id", Description: "One live evaluation invocation", Required: true},
		{Name: "repeat_index", Description: "One-based repeat index", Required: true},
		{Name: "result_identity", Description: "run_id + case_id + case_version + repeat_index", Required: true},
		{Name: "input_query", Description: "Current user query", Required: true},
		{Name: "interview_context", Description: "Interview context supplied to the Agent"},
		{Name: "history", Description: "Prior user and assistant messages"},
		{Name: "fixtures", Description: "Deterministic local tool fixtures"},
		{Name: "expected_tools", Description: "Expected tool behavior"},
		{Name: "expected_arguments", Description: "Expected tool arguments"},
		{Name: "expected_order", Description: "Expected tool order"},
		{Name: "required_facts", Description: "Required answer facts"},
		{Name: "forbidden_content", Description: "Forbidden answer content"},
		{Name: "judge_rubric", Description: "Local Judge rubric"},
		{Name: "actual_output", Description: "Candidate response"},
		{Name: "tool_trace", Description: "Sanitized tool invocation trace"},
		{Name: "execution_status", Description: "Local evaluation status"},
		{Name: "execution_error", Description: "Sanitized execution error category"},
		{Name: "infrastructure_failure", Description: "Whether this sample is invalid due to infrastructure or setup failure"},
		{Name: "duration_ms", Description: "Candidate execution duration in milliseconds"},
		{Name: "candidate_prompt_tokens", Description: "Candidate prompt token usage"},
		{Name: "candidate_completion_tokens", Description: "Candidate completion token usage"},
		{Name: "candidate_total_tokens", Description: "Candidate total token usage"},
		{Name: "candidate_trace_id", Description: "Candidate CozeLoop trace ID"},
		{Name: "judge_prompt_tokens", Description: "Judge prompt token usage"},
		{Name: "judge_completion_tokens", Description: "Judge completion token usage"},
		{Name: "judge_total_tokens", Description: "Judge total token usage"},
		{Name: "judge_trace_id", Description: "Judge CozeLoop trace ID"},
		{Name: "local_deterministic_score", Description: "Local deterministic score"},
		{Name: "local_hard_failure", Description: "Whether a critical deterministic check failed"},
		{Name: "local_judge_scores", Description: "Local Judge dimension scores"},
		{Name: "local_judge_rationale", Description: "Local Judge rationale"},
		{Name: "git_commit", Description: "Source commit associated with the run"},
		{Name: "candidate_model", Description: "Configured Candidate model"},
		{Name: "judge_model", Description: "Configured Judge model"},
		{Name: "prompt_key", Description: "Requested Prompt Hub key"},
		{Name: "prompt_version", Description: "Requested Prompt Hub version"},
		{Name: "prompt_label", Description: "Requested Prompt Hub label"},
		{Name: "prompt_source", Description: "Configured Prompt provider"},
		{Name: "prompt_resolved_key", Description: "Resolved Prompt key"},
		{Name: "prompt_resolved_version", Description: "Resolved Prompt version"},
		{Name: "prompt_resolved_label", Description: "Resolved Prompt label"},
		{Name: "prompt_resolved_source", Description: "Actual resolved Prompt provider"},
		{Name: "prompt_fallback", Description: "Whether Prompt resolution fell back to local"},
		{Name: "prompt_fallback_reason", Description: "Sanitized Prompt fallback reason"},
		{Name: "prompt_content_hash", Description: "SHA-256 digest of the resolved prompt messages"},
		{Name: "sync_content_hash", Description: "SHA-256 digest of the complete execution result fields"},
	}
	return DatasetSpec{
		Key:         EvaluationDatasetKey,
		Name:        EvaluationDatasetKey,
		Description: "Versioned local evaluation cases and execution results for the interview review Agent.",
		Fields:      fields,
	}
}

// Local report to dataset field mapping.
func BuildDatasetItems(cases []EvalCase, report RunReport) ([]DatasetItem, error) {
	if strings.TrimSpace(report.RunID) == "" {
		return nil, errors.New("evaluation report run ID is required")
	}
	caseByID := make(map[string]EvalCase, len(cases))
	for _, currentCase := range cases {
		caseByID[currentCase.ID] = currentCase
	}
	items := make([]DatasetItem, 0, len(report.Cases))
	seen := make(map[string]struct{}, len(report.Cases))
	for _, result := range report.Cases {
		currentCase, exists := caseByID[result.CaseID]
		if !exists {
			return nil, fmt.Errorf("result references unknown evaluation case %q", result.CaseID)
		}
		if result.CaseVersion != 0 && result.CaseVersion != currentCase.Version {
			return nil, fmt.Errorf("result case version does not match case %q", result.CaseID)
		}
		if result.RunID == "" || result.RunID != report.RunID || result.RepeatIndex < 1 {
			return nil, fmt.Errorf("result identity is incomplete or inconsistent for case %q", result.CaseID)
		}
		identity := fmt.Sprintf("%s:%s:%d:%d", result.RunID, result.CaseID, currentCase.Version, result.RepeatIndex)
		if _, duplicate := seen[identity]; duplicate {
			return nil, fmt.Errorf("duplicate result identity %q", identity)
		}
		seen[identity] = struct{}{}
		fields, err := datasetFields(currentCase, result, report, identity)
		if err != nil {
			return nil, fmt.Errorf("map result %q: %w", identity, err)
		}
		content, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("hash result %q: %w", identity, err)
		}
		contentHash := sha256.Sum256(content)
		contentHashHex := hex.EncodeToString(contentHash[:])
		fields["sync_content_hash"] = contentHashHex
		identityHash := sha256.Sum256([]byte(identity))
		items = append(items, DatasetItem{
			ItemKey:     "result-" + hex.EncodeToString(identityHash[:]),
			Identity:    identity,
			ContentHash: contentHashHex,
			Fields:      fields,
		})
	}
	return items, nil
}

func datasetFields(currentCase EvalCase, result CaseResult, report RunReport, identity string) (map[string]string, error) {
	forbiddenContent := append([]string(nil), currentCase.AnswerExpectations.MustNotContain...)
	forbiddenContent = append(forbiddenContent, currentCase.AnswerExpectations.CriticalForbid...)
	fields := map[string]string{
		"case_id":                currentCase.ID,
		"case_version":           strconv.Itoa(currentCase.Version),
		"tags":                   marshalDatasetValue(currentCase.Tags),
		"run_id":                 result.RunID,
		"repeat_index":           strconv.Itoa(result.RepeatIndex),
		"result_identity":        identity,
		"input_query":            currentCase.Input.Query,
		"interview_context":      currentCase.Input.InterviewContext,
		"history":                marshalDatasetValue(currentCase.Input.History),
		"fixtures":               marshalDatasetValue(currentCase.Fixtures),
		"expected_tools":         marshalDatasetValue(currentCase.ToolExpectations),
		"expected_arguments":     marshalDatasetValue(currentCase.ToolExpectations.Arguments),
		"expected_order":         marshalDatasetValue(currentCase.ToolExpectations.Ordered),
		"required_facts":         marshalDatasetValue(currentCase.AnswerExpectations.MustContain),
		"forbidden_content":      marshalDatasetValue(forbiddenContent),
		"judge_rubric":           currentCase.AnswerExpectations.JudgeRubric,
		"actual_output":          result.Answer,
		"tool_trace":             marshalDatasetValue(sanitizeToolTrace(result.ToolTrace)),
		"execution_status":       result.Status,
		"execution_error":        safeExecutionError(result.Error),
		"infrastructure_failure": strconv.FormatBool(result.InfrastructureFailure),
		"duration_ms":            strconv.FormatInt(result.DurationMS, 10),
		"candidate_trace_id":     result.Candidate.TraceID,
		"local_hard_failure":     strconv.FormatBool(HasCriticalFailure(result.HardChecks)),
		"candidate_model":        report.CandidateModel,
		"judge_model":            report.JudgeModel,
	}
	if len(result.HardChecks) > 0 {
		fields["local_deterministic_score"] = strconv.FormatFloat(result.Score.DeterministicScore, 'f', 2, 64)
	}
	if result.Judge != nil {
		fields["local_judge_scores"] = marshalDatasetValue(result.Judge.Scores)
		fields["local_judge_rationale"] = result.Judge.Scores.Rationale
	}
	if result.Candidate.Usage != nil {
		fields["candidate_prompt_tokens"] = strconv.Itoa(result.Candidate.Usage.PromptTokens)
		fields["candidate_completion_tokens"] = strconv.Itoa(result.Candidate.Usage.CompletionTokens)
		fields["candidate_total_tokens"] = strconv.Itoa(result.Candidate.Usage.TotalTokens)
	}
	if result.JudgeTrace != nil {
		fields["judge_trace_id"] = result.JudgeTrace.TraceID
		if result.JudgeTrace.Usage != nil {
			fields["judge_prompt_tokens"] = strconv.Itoa(result.JudgeTrace.Usage.PromptTokens)
			fields["judge_completion_tokens"] = strconv.Itoa(result.JudgeTrace.Usage.CompletionTokens)
			fields["judge_total_tokens"] = strconv.Itoa(result.JudgeTrace.Usage.TotalTokens)
		}
	}
	if report.Prompt != nil {
		fields["prompt_key"] = report.Prompt.Key
		fields["prompt_version"] = report.Prompt.Version
		fields["prompt_label"] = report.Prompt.Label
		fields["prompt_source"] = report.Prompt.Source
	}
	if report.GitCommit != "" {
		fields["git_commit"] = report.GitCommit
	}
	if result.PromptResolution != nil {
		prompt := result.PromptResolution
		fields["prompt_resolved_key"] = prompt.Resolved.Key
		fields["prompt_resolved_version"] = prompt.Resolved.Version
		fields["prompt_resolved_label"] = prompt.Resolved.Label
		fields["prompt_resolved_source"] = prompt.Resolved.Source
		fields["prompt_fallback"] = strconv.FormatBool(prompt.Fallback)
		fields["prompt_fallback_reason"] = prompt.FallbackReason
		fields["prompt_content_hash"] = prompt.ContentHash
	}
	return fields, nil
}

func marshalDatasetValue(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

func sanitizeToolTrace(trace []ToolTrace) []map[string]any {
	result := make([]map[string]any, len(trace))
	for i, item := range trace {
		// The structure evaluator only needs an object at `arguments`; argument
		// values may contain user data or credentials and are intentionally dropped.
		result[i] = map[string]any{"name": item.Name, "arguments": map[string]any{}}
		if item.Error != "" {
			result[i]["error"] = safeExecutionError(item.Error)
		}
	}
	return result
}

func safeExecutionError(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "judge:") {
		return "judge_error"
	}
	switch value {
	case "timeout", "cancelled", "judge_error", "tool_error", "model_auth_error", "execution_error", "invalid_run_identity", "candidate model is required":
		return value
	default:
		return "execution_error"
	}
}

// Dataset identity, idempotency, and sync workflow.
type DatasetSyncPayload struct {
	RunID         string             `json:"runId"`
	Dataset       DatasetSpec        `json:"dataset"`
	Version       DatasetVersionSpec `json:"version"`
	LegacyVersion string             `json:"-"`
	Items         []DatasetItem      `json:"items"`
}

type DatasetSyncResult struct {
	Dataset       Dataset              `json:"dataset"`
	Version       DatasetVersionRecord `json:"version"`
	UploadedItems int                  `json:"uploadedItems"`
	ReusedItems   int                  `json:"reusedItems"`
}

const (
	evaluationDatasetVersionPrefix = "0.0.0+run."
	maxEvaluationDatasetVersionLen = 50
)

var (
	safeEvaluationRunIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	semverBuildIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
)

// EvaluationDatasetVersionForRun encodes a run ID in SemVer build metadata,
// matching the public evaluation-set version contract. IDs accepted by the
// pending-payload format but not directly representable are mapped to a stable
// 160-bit digest; the full run ID remains in the payload and item identities.
func EvaluationDatasetVersionForRun(runID string) (string, error) {
	if !safeEvaluationRunIDPattern.MatchString(runID) {
		return "", errors.New("evaluation dataset run ID is invalid")
	}
	if semverBuildIdentifierPattern.MatchString(runID) && len(evaluationDatasetVersionPrefix)+len(runID) <= maxEvaluationDatasetVersionLen {
		return evaluationDatasetVersionPrefix + runID, nil
	}
	digest := sha256.Sum256([]byte(runID))
	return evaluationDatasetVersionPrefix + hex.EncodeToString(digest[:20]), nil
}

// SyncEvaluationDataset uses stable item keys and content hashes to make a
// repeated publish of the same run a no-op. A reused key with different content
// is rejected; results are immutable, and a new execution must have a new run ID.
func SyncEvaluationDataset(ctx context.Context, client EvaluationPlatform, payload DatasetSyncPayload) (DatasetSyncResult, error) {
	if client == nil {
		return DatasetSyncResult{}, errors.New("evaluation platform client is required")
	}
	if strings.TrimSpace(payload.RunID) == "" || len(payload.Items) == 0 {
		return DatasetSyncResult{}, errors.New("evaluation dataset run ID and items are required")
	}
	expectedVersion, err := EvaluationDatasetVersionForRun(payload.RunID)
	if err != nil {
		return DatasetSyncResult{}, err
	}
	if payload.Version.Version != expectedVersion {
		return DatasetSyncResult{}, errors.New("evaluation dataset version does not match the run ID")
	}
	seenKeys := make(map[string]struct{}, len(payload.Items))
	seenIdentities := make(map[string]struct{}, len(payload.Items))
	for _, item := range payload.Items {
		if item.ItemKey == "" || item.Identity == "" || item.ContentHash == "" || !strings.HasPrefix(item.Identity, payload.RunID+":") {
			return DatasetSyncResult{}, errors.New("evaluation dataset item identity does not match the run ID")
		}
		if _, duplicate := seenKeys[item.ItemKey]; duplicate {
			return DatasetSyncResult{}, errors.New("evaluation dataset contains duplicate item keys")
		}
		if _, duplicate := seenIdentities[item.Identity]; duplicate {
			return DatasetSyncResult{}, errors.New("evaluation dataset contains duplicate item identities")
		}
		seenKeys[item.ItemKey] = struct{}{}
		seenIdentities[item.Identity] = struct{}{}
	}

	dataset, err := client.EnsureDataset(ctx, payload.Dataset)
	if err != nil {
		return DatasetSyncResult{}, fmt.Errorf("ensure evaluation dataset: %w", err)
	}
	if dataset.ID == "" || dataset.Key != payload.Dataset.Key {
		return DatasetSyncResult{}, errors.New("evaluation platform returned an unexpected dataset reference")
	}
	versions, err := client.ListDatasetVersions(ctx, dataset.ID)
	if err != nil {
		return DatasetSyncResult{}, fmt.Errorf("list evaluation dataset versions: %w", err)
	}
	if payload.LegacyVersion != "" && payload.LegacyVersion != "run-"+payload.RunID {
		return DatasetSyncResult{}, errors.New("legacy evaluation dataset version does not match the run ID")
	}
	versionNames := []string{payload.Version.Version}
	if payload.LegacyVersion != "" {
		versionNames = append(versionNames, payload.LegacyVersion)
	}
	for _, versionName := range versionNames {
		for _, version := range versions {
			if version.Version != versionName {
				continue
			}
			if version.ID == "" {
				return DatasetSyncResult{}, errors.New("evaluation platform returned an empty dataset version ID")
			}
			existing, err := client.ListDatasetItems(ctx, dataset.ID, version.ID)
			if err != nil {
				return DatasetSyncResult{}, fmt.Errorf("verify existing evaluation dataset version: %w", err)
			}
			if err := compareDatasetItems(payload.Items, existing); err != nil {
				return DatasetSyncResult{}, fmt.Errorf("existing evaluation dataset version conflicts with this run: %w", err)
			}
			return DatasetSyncResult{Dataset: dataset, Version: version, ReusedItems: len(payload.Items)}, nil
		}
	}

	currentItems, err := client.ListDatasetItems(ctx, dataset.ID, "")
	if err != nil {
		return DatasetSyncResult{}, fmt.Errorf("list current evaluation dataset items: %w", err)
	}
	currentByKey := make(map[string]DatasetItem, len(currentItems))
	for _, item := range currentItems {
		currentByKey[item.ItemKey] = item
	}
	toCreate := make([]DatasetItem, 0, len(payload.Items))
	reused := 0
	for _, item := range payload.Items {
		if existing, ok := currentByKey[item.ItemKey]; ok {
			if existing.ContentHash != item.ContentHash || existing.Identity != item.Identity {
				return DatasetSyncResult{}, fmt.Errorf("evaluation item key %q already exists with different content", item.ItemKey)
			}
			reused++
			continue
		}
		toCreate = append(toCreate, item)
	}
	if len(toCreate) > 0 {
		writeResults, err := client.BatchCreateDatasetItems(ctx, dataset.ID, toCreate)
		if err != nil {
			return DatasetSyncResult{}, fmt.Errorf("write evaluation dataset items: %w", err)
		}
		if len(writeResults) != len(toCreate) {
			return DatasetSyncResult{}, errors.New("evaluation platform returned an incomplete item write result")
		}
	}

	// Verify data before creating an immutable version. If the process is retried
	// after a partial write, the stable item keys let it continue without silently
	// replacing content.
	verified, err := client.ListDatasetItems(ctx, dataset.ID, "")
	if err != nil {
		return DatasetSyncResult{}, fmt.Errorf("verify evaluation dataset items: %w", err)
	}
	if err := compareDatasetItems(payload.Items, verified); err != nil {
		return DatasetSyncResult{}, fmt.Errorf("evaluation dataset item verification failed: %w", err)
	}
	version, err := client.CreateDatasetVersion(ctx, dataset.ID, payload.Version)
	if err != nil {
		return DatasetSyncResult{}, fmt.Errorf("create evaluation dataset version: %w", err)
	}
	if version.ID == "" || version.Version != payload.Version.Version {
		return DatasetSyncResult{}, errors.New("evaluation platform returned an unexpected dataset version reference")
	}
	return DatasetSyncResult{
		Dataset:       dataset,
		Version:       version,
		UploadedItems: len(toCreate),
		ReusedItems:   reused,
	}, nil
}

func compareDatasetItems(expected, actual []DatasetItem) error {
	actualByKey := make(map[string]DatasetItem, len(actual))
	for _, item := range actual {
		actualByKey[item.ItemKey] = item
	}
	for _, item := range expected {
		found, ok := actualByKey[item.ItemKey]
		if !ok {
			return fmt.Errorf("item %q is missing", item.ItemKey)
		}
		if found.ContentHash != item.ContentHash || found.Identity != item.Identity {
			return fmt.Errorf("item %q does not match its local content hash", item.ItemKey)
		}
	}
	return nil
}
