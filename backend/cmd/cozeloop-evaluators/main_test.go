package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

func TestValidateManifestDoesNotLoadCozeLoopCredentials(t *testing.T) {
	manifestPath, _ := writeCLIInputs(t)
	configLoads, platformCreates := 0, 0
	var output strings.Builder
	err := run(context.Background(), []string{"validate", "--manifest", manifestPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) {
			configLoads++
			return config.Config{}, errors.New("credentials must not be required")
		},
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			platformCreates++
			return nil, errors.New("network must not be used")
		},
	})
	if err != nil {
		t.Fatalf("validate run() error = %v", err)
	}
	if configLoads != 0 || platformCreates != 0 {
		t.Fatalf("local validate loaded config %d times and created %d platform clients", configLoads, platformCreates)
	}
	if !strings.Contains(output.String(), "validated 1 evaluator definitions") {
		t.Fatalf("validate output = %q", output.String())
	}
}

func TestPublishWithoutApplyStopsBeforeReadingOrConnecting(t *testing.T) {
	configLoads, platformCreates := 0, 0
	err := run(context.Background(), []string{"publish", "--key", "answer-nonempty", "--inputs", "not-read.json"}, &strings.Builder{}, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) {
			configLoads++
			return config.Config{}, nil
		},
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			platformCreates++
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires --apply") {
		t.Fatalf("publish without --apply error = %v", err)
	}
	if configLoads != 0 || platformCreates != 0 {
		t.Fatalf("publish without --apply loaded config %d times and created %d clients", configLoads, platformCreates)
	}
}

func TestDebugUsesExplicitInputsAndRunsValidateBeforeBatch(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	platform := &evaluatorPlatformStub{}
	var output strings.Builder
	err := run(context.Background(), []string{"debug", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err != nil {
		t.Fatalf("debug run() error = %v", err)
	}
	if want := []string{"validate", "batch-debug"}; !reflect.DeepEqual(platform.calls, want) {
		t.Fatalf("remote calls = %#v, want %#v", platform.calls, want)
	}
	wantInput := eval.EvaluatorInputData{EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{
		"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: "candidate answer"},
	}}
	if !reflect.DeepEqual(platform.validatedInput, wantInput) || !reflect.DeepEqual(platform.batchedInputs, []eval.EvaluatorInputData{wantInput}) {
		t.Fatalf("debug inputs were not passed through as supplied: validate=%#v batch=%#v", platform.validatedInput, platform.batchedInputs)
	}
	if !strings.Contains(output.String(), "answer-nonempty input[0] score=1 reason=\"passed\"") {
		t.Fatalf("debug output = %q", output.String())
	}
}

func TestPublishRunsDebugBeforeAnyRemoteMutation(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	platform := &evaluatorPlatformStub{batchStatus: "fail", batchErrorCategory: "evaluator_error"}
	err := run(context.Background(), []string{"publish", "--apply", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &strings.Builder{}, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "evaluator execution failed") {
		t.Fatalf("publish with failed debug error = %v", err)
	}
	if want := []string{"validate", "batch-debug"}; !reflect.DeepEqual(platform.calls, want) {
		t.Fatalf("remote calls after failed debug = %#v, want %#v", platform.calls, want)
	}
}

func TestPublishApplyUpdatesDraftAndSubmitsOnlyAfterSuccessfulDebug(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	platform := &evaluatorPlatformStub{}
	var output strings.Builder
	err := run(context.Background(), []string{"publish", "--apply", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err != nil {
		t.Fatalf("publish run() error = %v", err)
	}
	want := []string{"validate", "batch-debug", "list-evaluators", "create-evaluator", "list-versions", "update-draft", "list-versions", "submit-version", "list-versions"}
	if !reflect.DeepEqual(platform.calls, want) {
		t.Fatalf("remote calls = %#v, want %#v", platform.calls, want)
	}
	if !strings.Contains(output.String(), "published evaluator answer-nonempty at version v1") {
		t.Fatalf("publish output = %q", output.String())
	}
}

func TestPublishReusesIdenticalVersionWithoutUpdatingDraft(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	manifest, err := eval.LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	contentHash, err := eval.EvaluatorPlatformContentHash(manifest.Evaluators[0])
	if err != nil {
		t.Fatal(err)
	}
	platform := &evaluatorPlatformStub{
		evaluatorExists: true,
		versions:        []eval.EvaluatorVersionReference{{ID: "version-1", Version: "v1", ContentHash: contentHash}},
	}
	var output strings.Builder
	err = run(context.Background(), []string{"publish", "--apply", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err != nil {
		t.Fatalf("publish existing version error = %v", err)
	}
	if containsCall(platform.calls, "update-draft") || containsCall(platform.calls, "submit-version") {
		t.Fatalf("matching immutable version was modified: calls=%#v", platform.calls)
	}
	if !strings.Contains(output.String(), "reused evaluator answer-nonempty at version v1") {
		t.Fatalf("publish output = %q", output.String())
	}
}

func TestPublishStopsOnFixedVersionContentConflict(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	platform := &evaluatorPlatformStub{
		evaluatorExists: true,
		versions:        []eval.EvaluatorVersionReference{{ID: "version-1", Version: "v1", ContentHash: "sha256:different"}},
	}
	err := run(context.Background(), []string{"publish", "--apply", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &strings.Builder{}, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) { return config.Config{}, nil },
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "content conflicts") {
		t.Fatalf("publish with version conflict error = %v", err)
	}
	if containsCall(platform.calls, "update-draft") || containsCall(platform.calls, "submit-version") {
		t.Fatalf("version conflict modified the remote evaluator: calls=%#v", platform.calls)
	}
}

func TestRemoteErrorsAndDebugReasonsRedactConfiguredAPIToken(t *testing.T) {
	manifestPath, inputsPath := writeCLIInputs(t)
	const apiToken = "secret-api-token"
	platform := &evaluatorPlatformStub{validateError: fmt.Errorf("upstream failed with %s", apiToken)}
	var output strings.Builder
	err := run(context.Background(), []string{"debug", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{CozeLoop: config.CozeLoopConfig{APIToken: apiToken}}, nil
		},
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) {
			return platform, nil
		},
	})
	if err == nil || strings.Contains(err.Error(), apiToken) {
		t.Fatalf("debug error = %v; API token must be redacted", err)
	}

	platform = &evaluatorPlatformStub{batchReason: "reason contains " + apiToken}
	output.Reset()
	err = run(context.Background(), []string{"debug", "--manifest", manifestPath, "--key", "answer-nonempty", "--inputs", inputsPath}, &output, &strings.Builder{}, cliDependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{CozeLoop: config.CozeLoopConfig{APIToken: apiToken}}, nil
		},
		newPlatform: func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error) { return platform, nil },
	})
	if err != nil {
		t.Fatalf("debug with successful batch error = %v", err)
	}
	if strings.Contains(output.String(), apiToken) || !strings.Contains(output.String(), "[REDACTED]") {
		t.Fatalf("debug output did not safely redact API token: %q", output.String())
	}
}

func TestLoadDebugInputsRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field": `[{"credentials":"secret"}]`,
		"trailing data": `[] {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inputs.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadDebugInputs(path); err == nil {
				t.Fatal("loadDebugInputs() unexpectedly succeeded")
			}
		})
	}
}

func writeCLIInputs(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	manifest := eval.EvaluatorManifest{
		Version: 1,
		Evaluators: []eval.EvaluatorDefinition{{
			Key:       "answer-nonempty",
			Name:      "CLI test evaluator",
			Type:      eval.EvaluatorTypeCode,
			Version:   "v1",
			AssetFile: "answer.py",
			InputSchemas: []eval.EvaluatorFieldSchema{{
				Key: "actual_output", Type: "string", Required: true,
			}},
			OutputSchemas: []eval.EvaluatorFieldSchema{
				{Key: "score", Type: "number", Required: true},
				{Key: "reason", Type: "string", Required: true},
			},
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "evaluators.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "answer.py"), []byte("def evaluate(inputs): return {'score': 1, 'reason': 'passed'}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inputsData, err := json.Marshal([]eval.EvaluatorInputData{{
		EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{
			"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: "candidate answer"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	inputsPath := filepath.Join(directory, "inputs.json")
	if err := os.WriteFile(inputsPath, inputsData, 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath, inputsPath
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

type evaluatorPlatformStub struct {
	calls              []string
	evaluatorExists    bool
	versions           []eval.EvaluatorVersionReference
	definition         eval.EvaluatorDefinition
	validateError      error
	validationValid    bool
	validationStatus   string
	validationScore    float64
	validationReason   string
	batchStatus        string
	batchErrorCategory string
	batchReason        string
	validatedInput     eval.EvaluatorInputData
	batchedInputs      []eval.EvaluatorInputData
}

func (platform *evaluatorPlatformStub) ListEvaluators(context.Context, eval.EvaluatorListFilter) ([]eval.EvaluatorReference, error) {
	platform.calls = append(platform.calls, "list-evaluators")
	if platform.evaluatorExists {
		return []eval.EvaluatorReference{{ID: "evaluator-1", Name: "CLI test evaluator", Type: eval.EvaluatorTypeCode}}, nil
	}
	return nil, nil
}

func (platform *evaluatorPlatformStub) CreateEvaluator(_ context.Context, definition eval.EvaluatorDefinition) (eval.EvaluatorReference, error) {
	platform.calls = append(platform.calls, "create-evaluator")
	platform.evaluatorExists = true
	platform.definition = definition
	return eval.EvaluatorReference{ID: "evaluator-1", Name: definition.Name, Type: definition.Type}, nil
}

func (platform *evaluatorPlatformStub) UpdateEvaluatorDraft(_ context.Context, _ string, definition eval.EvaluatorDefinition) (eval.EvaluatorReference, error) {
	platform.calls = append(platform.calls, "update-draft")
	platform.definition = definition
	return eval.EvaluatorReference{ID: "evaluator-1", Name: definition.Name, Type: definition.Type}, nil
}

func (platform *evaluatorPlatformStub) ListEvaluatorVersions(context.Context, string) ([]eval.EvaluatorVersionReference, error) {
	platform.calls = append(platform.calls, "list-versions")
	return append([]eval.EvaluatorVersionReference(nil), platform.versions...), nil
}

func (platform *evaluatorPlatformStub) SubmitEvaluatorVersion(_ context.Context, _, version, _ string) (eval.EvaluatorVersionReference, error) {
	platform.calls = append(platform.calls, "submit-version")
	contentHash, err := eval.EvaluatorPlatformContentHash(platform.definition)
	if err != nil {
		return eval.EvaluatorVersionReference{}, err
	}
	reference := eval.EvaluatorVersionReference{ID: "version-1", Version: version, ContentHash: contentHash}
	platform.versions = append(platform.versions, reference)
	return reference, nil
}

func (platform *evaluatorPlatformStub) ValidateEvaluator(_ context.Context, _ eval.EvaluatorDefinition, input eval.EvaluatorInputData) (eval.EvaluatorValidationResult, error) {
	platform.calls = append(platform.calls, "validate")
	platform.validatedInput = input
	if platform.validateError != nil {
		return eval.EvaluatorValidationResult{}, platform.validateError
	}
	valid := platform.validationValid
	if !platform.validationValid && platform.validationStatus == "" {
		valid = true
	}
	status := platform.validationStatus
	if status == "" {
		status = "success"
	}
	score := platform.validationScore
	if score == 0 {
		score = 1
	}
	reason := platform.validationReason
	if reason == "" {
		reason = "passed"
	}
	return eval.EvaluatorValidationResult{Valid: valid, Result: &eval.EvaluatorExecutionResult{Status: status, Score: &score, Reason: reason}}, nil
}

func (platform *evaluatorPlatformStub) BatchDebugEvaluator(_ context.Context, _ eval.EvaluatorDefinition, inputs []eval.EvaluatorInputData) ([]eval.EvaluatorDebugResult, error) {
	platform.calls = append(platform.calls, "batch-debug")
	platform.batchedInputs = append([]eval.EvaluatorInputData(nil), inputs...)
	results := make([]eval.EvaluatorDebugResult, 0, len(inputs))
	for index := range inputs {
		score := 1.0
		reason := platform.batchReason
		if reason == "" {
			reason = "passed"
		}
		status := platform.batchStatus
		if status == "" {
			status = "success"
		}
		results = append(results, eval.EvaluatorDebugResult{InputIndex: index, Result: eval.EvaluatorExecutionResult{
			Status: status, Score: &score, Reason: reason, ErrorCategory: platform.batchErrorCategory,
		}})
	}
	return results, nil
}
