package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"strings"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
	"interview-memory-agent/backend/internal/infrastructure/cozeloop"
)

const (
	defaultManifestPath    = "../evals/cozeloop/evaluators.v1.json"
	maxDebugInputFileBytes = 4 << 20
	maxDebugInputs         = 100
)

type cliDependencies struct {
	loadConfig  func() (config.Config, error)
	newPlatform func(config.CozeLoopConfig) (eval.EvaluatorPlatform, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dependencies := cliDependencies{
		loadConfig:  config.Load,
		newPlatform: cozeloop.NewEvaluatorClient,
	}
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, dependencies); err != nil {
		fmt.Fprintln(os.Stderr, "cozeloop-evaluators:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies cliDependencies) error {
	if len(args) == 0 {
		return errors.New("usage: cozeloop-evaluators <validate|debug|publish> [flags]")
	}
	action := args[0]
	if action != "validate" && action != "debug" && action != "publish" {
		return errors.New("action must be validate, debug, or publish")
	}

	flags := flag.NewFlagSet("cozeloop-evaluators "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		fmt.Fprintf(stderr, "Usage: cozeloop-evaluators %s [--manifest path] [--key key] [--inputs json-file]", action)
		if action == "publish" {
			fmt.Fprint(stderr, " --apply")
		}
		fmt.Fprintln(stderr)
	}
	manifestPath := flags.String("manifest", defaultManifestPath, "evaluator manifest path")
	key := flags.String("key", "", "one evaluator key (required for debug and publish)")
	inputsPath := flags.String("inputs", "", "local JSON array of EvaluatorInputData for debug and publish")
	apply := flags.Bool("apply", false, "allow publish to create/update remote evaluator metadata and submit a version")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid evaluator CLI flags")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if action == "publish" && !*apply {
		return errors.New("publish requires --apply; no remote requests were made")
	}
	if action != "publish" && *apply {
		return errors.New("--apply is only valid with publish")
	}
	if action == "validate" && *inputsPath != "" {
		return errors.New("--inputs is only valid with debug or publish")
	}

	manifest, err := eval.LoadEvaluatorManifestFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("validate local evaluator manifest: %w", err)
	}
	if action == "validate" {
		definitions, err := selectLocalDefinitions(manifest, *key)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "validated %d evaluator definitions\n", len(definitions))
		for _, definition := range definitions {
			fmt.Fprintf(stdout, "- %s (%s, version %s)\n", definition.Key, definition.Type, definition.Version)
		}
		return nil
	}

	definition, err := selectRemoteDefinition(manifest, *key)
	if err != nil {
		return err
	}
	inputs, err := loadDebugInputs(*inputsPath)
	if err != nil {
		return err
	}
	if dependencies.loadConfig == nil || dependencies.newPlatform == nil {
		return errors.New("remote evaluator CLI dependencies are unavailable")
	}
	cfg, err := dependencies.loadConfig()
	if err != nil {
		return fmt.Errorf("load CozeLoop configuration: %w", err)
	}
	platform, err := dependencies.newPlatform(cfg.CozeLoop)
	if err != nil {
		return redactCLIError(fmt.Errorf("configure evaluator platform: %w", err), cfg.CozeLoop.APIToken)
	}
	if platform == nil {
		return errors.New("evaluator platform is unavailable")
	}

	results, err := debugEvaluator(ctx, platform, definition, inputs)
	if err != nil {
		return redactCLIError(err, cfg.CozeLoop.APIToken)
	}
	writeDebugResults(stdout, definition, results, cfg.CozeLoop.APIToken)
	if action == "debug" {
		return nil
	}

	evaluator, err := eval.EnsureEvaluatorResource(ctx, platform, definition)
	if err != nil {
		return redactCLIError(err, cfg.CozeLoop.APIToken)
	}
	version, exists, err := eval.FindEvaluatorVersion(ctx, platform, evaluator.ID, definition)
	if err != nil {
		return redactCLIError(err, cfg.CozeLoop.APIToken)
	}
	if !exists {
		if _, err := platform.UpdateEvaluatorDraft(ctx, evaluator.ID, definition); err != nil {
			return redactCLIError(fmt.Errorf("update evaluator draft: %w", err), cfg.CozeLoop.APIToken)
		}
		version, err = eval.EnsureEvaluatorVersion(ctx, platform, evaluator.ID, definition)
		if err != nil {
			return redactCLIError(err, cfg.CozeLoop.APIToken)
		}
	}
	state := "published"
	if exists {
		state = "reused"
	}
	fmt.Fprintf(stdout, "%s evaluator %s at version %s\n", state, definition.Key, version.Version)
	return nil
}

func selectLocalDefinitions(manifest eval.EvaluatorManifest, key string) ([]eval.EvaluatorDefinition, error) {
	if key == "" {
		return manifest.Evaluators, nil
	}
	definition, err := findEvaluatorDefinition(manifest, key)
	if err != nil {
		return nil, err
	}
	return []eval.EvaluatorDefinition{definition}, nil
}

func selectRemoteDefinition(manifest eval.EvaluatorManifest, key string) (eval.EvaluatorDefinition, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return eval.EvaluatorDefinition{}, errors.New("--key is required for remote evaluator actions")
	}
	return findEvaluatorDefinition(manifest, key)
}

func findEvaluatorDefinition(manifest eval.EvaluatorManifest, key string) (eval.EvaluatorDefinition, error) {
	for _, definition := range manifest.Evaluators {
		if definition.Key == key {
			return definition, nil
		}
	}
	return eval.EvaluatorDefinition{}, fmt.Errorf("evaluator key %q is not present in the manifest", key)
}

func loadDebugInputs(path string) ([]eval.EvaluatorInputData, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--inputs is required for remote evaluator actions")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open evaluator debug input file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDebugInputFileBytes+1))
	if err != nil {
		return nil, errors.New("cannot read evaluator debug input file")
	}
	if len(data) > maxDebugInputFileBytes {
		return nil, errors.New("evaluator debug input file exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var inputs []eval.EvaluatorInputData
	if err := decoder.Decode(&inputs); err != nil {
		return nil, errors.New("evaluator debug input file must contain a JSON array of input_data objects")
	}
	if err := evaluatorJSONEOF(decoder); err != nil {
		return nil, errors.New("evaluator debug input file contains trailing data")
	}
	if len(inputs) == 0 {
		return nil, errors.New("evaluator debug input file must contain at least one input")
	}
	if len(inputs) > maxDebugInputs {
		return nil, fmt.Errorf("evaluator debug input count exceeds the limit of %d", maxDebugInputs)
	}
	return inputs, nil
}

func evaluatorJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func debugEvaluator(ctx context.Context, platform eval.EvaluatorPlatform, definition eval.EvaluatorDefinition, inputs []eval.EvaluatorInputData) ([]eval.EvaluatorDebugResult, error) {
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

func validateDebugResult(result *eval.EvaluatorExecutionResult) error {
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

func writeDebugResults(output io.Writer, definition eval.EvaluatorDefinition, results []eval.EvaluatorDebugResult, token string) {
	for _, result := range results {
		reason := strings.TrimSpace(result.Result.Reason)
		if token != "" {
			reason = strings.ReplaceAll(reason, token, "[REDACTED]")
		}
		fmt.Fprintf(output, "%s input[%d] score=%.4g reason=%q\n", definition.Key, result.InputIndex, *result.Result.Score, reason)
	}
}

func redactCLIError(err error, token string) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if token != "" {
		message = strings.ReplaceAll(message, token, "[REDACTED]")
	}
	return errors.New(message)
}
