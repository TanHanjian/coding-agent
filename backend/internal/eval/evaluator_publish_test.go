package eval

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPublishEvaluatorUpdatesDraftBeforeSubmittingMissingVersion(t *testing.T) {
	platform := &publishEvaluatorPlatformStub{}
	definition := publishTestDefinition()

	result, err := PublishEvaluator(context.Background(), platform, definition)
	if err != nil {
		t.Fatalf("PublishEvaluator() error = %v", err)
	}
	wantCalls := []string{
		"list-evaluators",
		"create-evaluator",
		"list-versions",
		"update-draft",
		"list-versions",
		"submit-version",
		"list-versions",
	}
	if !reflect.DeepEqual(platform.calls, wantCalls) {
		t.Fatalf("platform calls = %#v, want %#v", platform.calls, wantCalls)
	}
	if result.Reused || result.Version.Version != definition.Version {
		t.Fatalf("PublishEvaluator() result = %#v, want newly published version", result)
	}
}

func TestPublishEvaluatorReusesMatchingImmutableVersion(t *testing.T) {
	definition := publishTestDefinition()
	contentHash, err := EvaluatorPlatformContentHash(definition)
	if err != nil {
		t.Fatal(err)
	}
	platform := &publishEvaluatorPlatformStub{
		evaluatorExists: true,
		versions:        []EvaluatorVersionReference{{ID: "version-1", Version: definition.Version, ContentHash: contentHash}},
	}

	result, err := PublishEvaluator(context.Background(), platform, definition)
	if err != nil {
		t.Fatalf("PublishEvaluator() error = %v", err)
	}
	if !result.Reused || result.Version.ID != "version-1" {
		t.Fatalf("PublishEvaluator() result = %#v, want reused version", result)
	}
	if containsPublishCall(platform.calls, "update-draft") || containsPublishCall(platform.calls, "submit-version") {
		t.Fatalf("matching version was mutated: calls=%#v", platform.calls)
	}
}

func TestPublishEvaluatorStopsOnImmutableVersionConflict(t *testing.T) {
	definition := publishTestDefinition()
	platform := &publishEvaluatorPlatformStub{
		evaluatorExists: true,
		versions:        []EvaluatorVersionReference{{ID: "version-1", Version: definition.Version, ContentHash: "sha256:conflict"}},
	}

	_, err := PublishEvaluator(context.Background(), platform, definition)
	if err == nil || !strings.Contains(err.Error(), "content conflicts") {
		t.Fatalf("PublishEvaluator() error = %v, want content conflict", err)
	}
	if containsPublishCall(platform.calls, "update-draft") || containsPublishCall(platform.calls, "submit-version") {
		t.Fatalf("conflicting version was mutated: calls=%#v", platform.calls)
	}
}

func publishTestDefinition() EvaluatorDefinition {
	return EvaluatorDefinition{
		Key:       "publish-test",
		Name:      "Publish test evaluator",
		Type:      EvaluatorTypeCode,
		Version:   "v1",
		AssetFile: "publish.py",
		AssetContent: "def exec_evaluation(turn):\n" +
			"    return EvalOutput(score=1, reason=\"passed\")\n",
		InputSchemas:  []EvaluatorFieldSchema{{Key: "actual_output", Type: "string", Required: true}},
		OutputSchemas: []EvaluatorFieldSchema{{Key: "score", Type: "number", Required: true}},
	}
}

type publishEvaluatorPlatformStub struct {
	calls           []string
	evaluatorExists bool
	versions        []EvaluatorVersionReference
	definition      EvaluatorDefinition
}

func (platform *publishEvaluatorPlatformStub) ListEvaluators(context.Context, EvaluatorListFilter) ([]EvaluatorReference, error) {
	platform.calls = append(platform.calls, "list-evaluators")
	if platform.evaluatorExists {
		return []EvaluatorReference{{ID: "evaluator-1", Name: "Publish test evaluator", Type: EvaluatorTypeCode}}, nil
	}
	return nil, nil
}

func (platform *publishEvaluatorPlatformStub) CreateEvaluator(_ context.Context, definition EvaluatorDefinition) (EvaluatorReference, error) {
	platform.calls = append(platform.calls, "create-evaluator")
	platform.evaluatorExists = true
	platform.definition = definition
	return EvaluatorReference{ID: "evaluator-1", Name: definition.Name, Type: definition.Type}, nil
}

func (platform *publishEvaluatorPlatformStub) UpdateEvaluatorDraft(_ context.Context, _ string, definition EvaluatorDefinition) (EvaluatorReference, error) {
	platform.calls = append(platform.calls, "update-draft")
	platform.definition = definition
	return EvaluatorReference{ID: "evaluator-1", Name: definition.Name, Type: definition.Type}, nil
}

func (platform *publishEvaluatorPlatformStub) ListEvaluatorVersions(context.Context, string) ([]EvaluatorVersionReference, error) {
	platform.calls = append(platform.calls, "list-versions")
	return append([]EvaluatorVersionReference(nil), platform.versions...), nil
}

func (platform *publishEvaluatorPlatformStub) SubmitEvaluatorVersion(_ context.Context, _, version, _ string) (EvaluatorVersionReference, error) {
	platform.calls = append(platform.calls, "submit-version")
	contentHash, err := EvaluatorPlatformContentHash(platform.definition)
	if err != nil {
		return EvaluatorVersionReference{}, err
	}
	result := EvaluatorVersionReference{ID: "version-1", Version: version, ContentHash: contentHash}
	platform.versions = append(platform.versions, result)
	return result, nil
}

func (platform *publishEvaluatorPlatformStub) ValidateEvaluator(context.Context, EvaluatorDefinition, EvaluatorInputData) (EvaluatorValidationResult, error) {
	return EvaluatorValidationResult{}, errors.New("not used")
}

func (platform *publishEvaluatorPlatformStub) BatchDebugEvaluator(context.Context, EvaluatorDefinition, []EvaluatorInputData) ([]EvaluatorDebugResult, error) {
	return nil, errors.New("not used")
}

func containsPublishCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

var _ EvaluatorPlatform = (*publishEvaluatorPlatformStub)(nil)
