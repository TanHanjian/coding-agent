package eval

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestDebugEvaluatorValidatesBeforeBatchDebug(t *testing.T) {
	platform := &debugEvaluatorPlatformStub{}
	inputs := []EvaluatorInputData{{InputFields: map[string]EvaluatorFieldContent{"actual_output": {ContentType: EvaluatorContentTypeText, Text: "answer"}}}}

	results, err := DebugEvaluator(context.Background(), platform, EvaluatorDefinition{}, inputs)
	if err != nil {
		t.Fatalf("DebugEvaluator() error = %v", err)
	}
	if want := []string{"validate", "batch-debug"}; !reflect.DeepEqual(platform.calls, want) {
		t.Fatalf("platform calls = %#v, want %#v", platform.calls, want)
	}
	if len(results) != 1 || results[0].InputIndex != 0 || results[0].Result.Score == nil || *results[0].Result.Score != 1 {
		t.Fatalf("DebugEvaluator() results = %#v, want one successful result", results)
	}
}

func TestDebugEvaluatorRejectsInvalidWorkflowResults(t *testing.T) {
	score := 1.0
	tests := []struct {
		name          string
		inputs        []EvaluatorInputData
		validation    EvaluatorValidationResult
		validationErr error
		batch         []EvaluatorDebugResult
		batchErr      error
		wantError     string
		wantCalls     []string
	}{
		{
			name:      "empty inputs",
			wantError: "evaluator debug requires at least one input",
		},
		{
			name:          "validation request fails",
			inputs:        []EvaluatorInputData{{}},
			validationErr: errors.New("validation unavailable"),
			wantError:     "remote evaluator validation failed: validation unavailable",
			wantCalls:     []string{"validate"},
		},
		{
			name:       "validation rejects",
			inputs:     []EvaluatorInputData{{}},
			validation: EvaluatorValidationResult{Valid: false, Result: &EvaluatorExecutionResult{}},
			wantError:  "remote evaluator validation rejected the definition or first input",
			wantCalls:  []string{"validate"},
		},
		{
			name:       "validation output is incomplete",
			inputs:     []EvaluatorInputData{{}},
			validation: EvaluatorValidationResult{Valid: true},
			wantError:  "remote evaluator validation did not produce a successful result: evaluator output is missing",
			wantCalls:  []string{"validate"},
		},
		{
			name:       "batch request fails",
			inputs:     []EvaluatorInputData{{}},
			validation: successfulValidation(score),
			batchErr:   errors.New("batch unavailable"),
			wantError:  "remote evaluator batch debug failed: batch unavailable",
			wantCalls:  []string{"validate", "batch-debug"},
		},
		{
			name:       "batch result count is incomplete",
			inputs:     []EvaluatorInputData{{}, {}},
			validation: successfulValidation(score),
			batch:      []EvaluatorDebugResult{{InputIndex: 0, Result: successfulExecution(score)}},
			wantError:  "remote evaluator batch debug returned an incomplete result set",
			wantCalls:  []string{"validate", "batch-debug"},
		},
		{
			name:       "batch index is invalid",
			inputs:     []EvaluatorInputData{{}},
			validation: successfulValidation(score),
			batch:      []EvaluatorDebugResult{{InputIndex: 1, Result: successfulExecution(score)}},
			wantError:  "remote evaluator batch debug returned an invalid input index",
			wantCalls:  []string{"validate", "batch-debug"},
		},
		{
			name:       "batch index is duplicated",
			inputs:     []EvaluatorInputData{{}, {}},
			validation: successfulValidation(score),
			batch: []EvaluatorDebugResult{
				{InputIndex: 0, Result: successfulExecution(score)},
				{InputIndex: 0, Result: successfulExecution(score)},
			},
			wantError: "remote evaluator batch debug returned a duplicate input index",
			wantCalls: []string{"validate", "batch-debug"},
		},
		{
			name:       "batch result is incomplete",
			inputs:     []EvaluatorInputData{{}},
			validation: successfulValidation(score),
			batch:      []EvaluatorDebugResult{{InputIndex: 0, Result: EvaluatorExecutionResult{Status: "success", Score: &score}}},
			wantError:  "remote evaluator debug input 0 failed: evaluator output must include a finite score and non-empty reason",
			wantCalls:  []string{"validate", "batch-debug"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			platform := &debugEvaluatorPlatformStub{
				validation:    test.validation,
				validationErr: test.validationErr,
				batch:         test.batch,
				batchErr:      test.batchErr,
			}
			_, err := DebugEvaluator(context.Background(), platform, EvaluatorDefinition{}, test.inputs)
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("DebugEvaluator() error = %v, want %q", err, test.wantError)
			}
			if !reflect.DeepEqual(platform.calls, test.wantCalls) {
				t.Fatalf("platform calls = %#v, want %#v", platform.calls, test.wantCalls)
			}
		})
	}
}

func successfulValidation(score float64) EvaluatorValidationResult {
	return EvaluatorValidationResult{Valid: true, Result: &EvaluatorExecutionResult{Status: "success", Score: &score, Reason: "validated"}}
}

func successfulExecution(score float64) EvaluatorExecutionResult {
	return EvaluatorExecutionResult{Status: "success", Score: &score, Reason: "passed"}
}

type debugEvaluatorPlatformStub struct {
	calls         []string
	validation    EvaluatorValidationResult
	validationErr error
	batch         []EvaluatorDebugResult
	batchErr      error
}

func (platform *debugEvaluatorPlatformStub) ValidateEvaluator(context.Context, EvaluatorDefinition, EvaluatorInputData) (EvaluatorValidationResult, error) {
	platform.calls = append(platform.calls, "validate")
	if platform.validationErr != nil {
		return EvaluatorValidationResult{}, platform.validationErr
	}
	if platform.validation.Result == nil && !platform.validation.Valid {
		return EvaluatorValidationResult{Valid: true, Result: &EvaluatorExecutionResult{Status: "success", Score: floatPointer(1), Reason: "validated"}}, nil
	}
	return platform.validation, nil
}

func (platform *debugEvaluatorPlatformStub) BatchDebugEvaluator(context.Context, EvaluatorDefinition, []EvaluatorInputData) ([]EvaluatorDebugResult, error) {
	platform.calls = append(platform.calls, "batch-debug")
	if platform.batchErr != nil {
		return nil, platform.batchErr
	}
	if platform.batch == nil {
		return []EvaluatorDebugResult{{InputIndex: 0, Result: successfulExecution(1)}}, nil
	}
	return platform.batch, nil
}

func (platform *debugEvaluatorPlatformStub) ListEvaluators(context.Context, EvaluatorListFilter) ([]EvaluatorReference, error) {
	return nil, errors.New("not used")
}

func (platform *debugEvaluatorPlatformStub) CreateEvaluator(context.Context, EvaluatorDefinition) (EvaluatorReference, error) {
	return EvaluatorReference{}, errors.New("not used")
}

func (platform *debugEvaluatorPlatformStub) UpdateEvaluatorDraft(context.Context, string, EvaluatorDefinition) (EvaluatorReference, error) {
	return EvaluatorReference{}, errors.New("not used")
}

func (platform *debugEvaluatorPlatformStub) ListEvaluatorVersions(context.Context, string) ([]EvaluatorVersionReference, error) {
	return nil, errors.New("not used")
}

func (platform *debugEvaluatorPlatformStub) SubmitEvaluatorVersion(context.Context, string, string, string) (EvaluatorVersionReference, error) {
	return EvaluatorVersionReference{}, errors.New("not used")
}

func floatPointer(value float64) *float64 {
	return &value
}

var _ EvaluatorPlatform = (*debugEvaluatorPlatformStub)(nil)
