package eval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// DebugEvaluator validates an evaluator with one input, then runs the complete
// batch. The validation-first order is part of the publish safety contract.
func DebugEvaluator(ctx context.Context, platform EvaluatorPlatform, definition EvaluatorDefinition, inputs []EvaluatorInputData) ([]EvaluatorDebugResult, error) {
	if len(inputs) == 0 {
		return nil, errors.New("evaluator debug requires at least one input")
	}
	validation, err := platform.ValidateEvaluator(ctx, definition, inputs[0])
	if err != nil {
		return nil, fmt.Errorf("remote evaluator validation failed: %w", err)
	}
	if !validation.Valid {
		return nil, errors.New("remote evaluator validation rejected the definition or first input")
	}
	if err := validateDebugResult(validation.Result); err != nil {
		return nil, fmt.Errorf("remote evaluator validation did not produce a successful result: %w", err)
	}
	results, err := platform.BatchDebugEvaluator(ctx, definition, inputs)
	if err != nil {
		return nil, fmt.Errorf("remote evaluator batch debug failed: %w", err)
	}
	if len(results) != len(inputs) {
		return nil, errors.New("remote evaluator batch debug returned an incomplete result set")
	}
	seenIndexes := make(map[int]struct{}, len(results))
	for _, result := range results {
		if result.InputIndex < 0 || result.InputIndex >= len(inputs) {
			return nil, errors.New("remote evaluator batch debug returned an invalid input index")
		}
		if _, duplicate := seenIndexes[result.InputIndex]; duplicate {
			return nil, errors.New("remote evaluator batch debug returned a duplicate input index")
		}
		seenIndexes[result.InputIndex] = struct{}{}
		if err := validateDebugResult(&result.Result); err != nil {
			return nil, fmt.Errorf("remote evaluator debug input %d failed: %w", result.InputIndex, err)
		}
	}
	return results, nil
}

// EvaluatorPublishResult describes the immutable evaluator version selected by a
// publish attempt. Reused is true when the exact version already existed.
type EvaluatorPublishResult struct {
	Evaluator EvaluatorReference
	Version   EvaluatorVersionReference
	Reused    bool
}

// PublishEvaluator ensures the evaluator resource and publishes one immutable
// version when the exact content is not already present. Debug validation is
// intentionally separate and must run before this mutating workflow.
func PublishEvaluator(ctx context.Context, platform EvaluatorPlatform, definition EvaluatorDefinition) (EvaluatorPublishResult, error) {
	evaluator, err := EnsureEvaluatorResource(ctx, platform, definition)
	if err != nil {
		return EvaluatorPublishResult{}, err
	}
	version, exists, err := FindEvaluatorVersion(ctx, platform, evaluator.ID, definition)
	if err != nil {
		return EvaluatorPublishResult{}, err
	}
	if exists {
		return EvaluatorPublishResult{Evaluator: evaluator, Version: version, Reused: true}, nil
	}
	if _, err := platform.UpdateEvaluatorDraft(ctx, evaluator.ID, definition); err != nil {
		return EvaluatorPublishResult{}, fmt.Errorf("update evaluator draft: %w", err)
	}
	version, err = EnsureEvaluatorVersion(ctx, platform, evaluator.ID, definition)
	if err != nil {
		return EvaluatorPublishResult{}, err
	}
	return EvaluatorPublishResult{Evaluator: evaluator, Version: version}, nil
}

func validateDebugResult(result *EvaluatorExecutionResult) error {
	if result == nil {
		return errors.New("evaluator output is missing")
	}
	if result.Status != "success" {
		if result.ErrorCategory != "" {
			return fmt.Errorf("evaluator execution failed (%s)", result.ErrorCategory)
		}
		return errors.New("evaluator execution failed")
	}
	if result.Score == nil || math.IsNaN(*result.Score) || math.IsInf(*result.Score, 0) || strings.TrimSpace(result.Reason) == "" {
		return errors.New("evaluator output must include a finite score and non-empty reason")
	}
	return nil
}
