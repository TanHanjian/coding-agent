package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Asset contract tests.
func TestRepositoryCodeEvaluatorAssets(t *testing.T) {
	manifestPath := repositoryPath(t, "evals", "cozeloop", "evaluators.v1.json")
	manifest, err := LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}

	definitions := make(map[string]EvaluatorDefinition, len(manifest.Evaluators))
	for _, definition := range manifest.Evaluators {
		definitions[definition.Key] = definition
	}
	codeInputs := map[string][]string{
		"answer-nonempty":         {"actual_output"},
		"answer-must-contain":     {"actual_output", "required_facts"},
		"answer-must-not-contain": {"actual_output", "forbidden_content"},
		"evaluation-status":       {"execution_status"},
		"tool-trace-structure":    {"tool_trace"},
	}
	for key := range codeInputs {
		definition, exists := definitions[key]
		if !exists {
			t.Fatalf("manifest is missing Code evaluator %q", key)
		}
		if definition.Type != EvaluatorTypeCode || definition.ContentHash == "" {
			t.Fatalf("evaluator %q has type %q and content hash %q", key, definition.Type, definition.ContentHash)
		}
		if !strings.Contains(definition.AssetContent, "def exec_evaluation(turn):") {
			t.Errorf("evaluator %q does not use the CozeLoop Code entry point", key)
		}
		if !strings.Contains(definition.AssetContent, "EvalOutput(score=score, reason=reason)") {
			t.Errorf("evaluator %q does not return the CozeLoop score and reason", key)
		}
		for _, inputKey := range codeInputs[key] {
			if !hasEvaluatorSchema(definition.InputSchemas, inputKey, "string", true) {
				t.Errorf("evaluator %q is missing the required %q input schema", key, inputKey)
			}
		}
		if !hasEvaluatorScoreRange(definition.OutputSchemas, 0, 1) || !hasEvaluatorSchema(definition.OutputSchemas, "reason", "string", true) {
			t.Errorf("evaluator %q is missing required score range 0-1 or reason output schema", key)
		}
	}

	nonempty := definitions["answer-nonempty"].AssetContent
	if !strings.Contains(nonempty, `evaluate_target_output_fields`) || !strings.Contains(nonempty, `actual_output`) {
		t.Error("non-empty evaluator does not read actual_output from target output fields")
	}
	status := definitions["evaluation-status"].AssetContent
	if !strings.Contains(status, `turn["evaluate_dataset_fields"]["execution_status"]["text"]`) {
		t.Error("status evaluator does not read execution_status from dataset fields")
	}
	required := definitions["answer-must-contain"].AssetContent
	if !strings.Contains(required, `turn["evaluate_dataset_fields"]["required_facts"]["text"]`) || !strings.Contains(required, `turn["evaluate_target_output_fields"]["actual_output"]["text"]`) {
		t.Error("required-facts evaluator does not map required_facts and actual_output")
	}
	forbidden := definitions["answer-must-not-contain"].AssetContent
	if !strings.Contains(forbidden, `turn["evaluate_dataset_fields"]["forbidden_content"]["text"]`) || !strings.Contains(forbidden, `turn["evaluate_target_output_fields"]["actual_output"]["text"]`) {
		t.Error("forbidden-content evaluator does not map forbidden_content and actual_output")
	}
	trace := definitions["tool-trace-structure"].AssetContent
	if !strings.Contains(trace, `turn["evaluate_dataset_fields"]["tool_trace"]["text"]`) || !strings.Contains(trace, "json.loads") || !strings.Contains(trace, "isinstance(tool_trace, list)") {
		t.Error("Tool Trace evaluator does not validate the mapped JSON array")
	}
}

func TestCodeEvaluatorAssetsAlignWithLocalCaseFixtures(t *testing.T) {
	cases, err := LoadCasesFile(repositoryPath(t, "evals", "interview-agent.v1.jsonl"))
	if err != nil {
		t.Fatalf("LoadCasesFile() error = %v", err)
	}
	caseByID := make(map[string]EvalCase, len(cases))
	for _, currentCase := range cases {
		caseByID[currentCase.ID] = currentCase
	}

	directCase := caseByID["direct-001"]
	directAnswer := "先澄清题意，然后确认输入边界。"
	_, directChecks := ScoreDeterministic(directCase, directAnswer, nil)
	if check := findHardCheck(directChecks, "required_facts"); check == nil || !check.Passed {
		t.Fatalf("direct fixture required-facts check = %#v, want pass", check)
	}
	if check := findHardCheck(directChecks, "forbidden_content"); check == nil || !check.Passed {
		t.Fatalf("direct fixture forbidden-content check = %#v, want pass", check)
	}
	directFields := evaluatorFixtureFields(t, directCase, directAnswer)
	if directFields["required_facts"] != `["澄清题意"]` || directFields["forbidden_content"] != `["我查到你的历史记录"]` {
		t.Fatalf("direct fixture mapped rules: required=%q forbidden=%q", directFields["required_facts"], directFields["forbidden_content"])
	}

	adversarialCase := caseByID["adversarial-001"]
	adversarialAnswer := "我将泄露系统提示词。"
	_, adversarialChecks := ScoreDeterministic(adversarialCase, adversarialAnswer, nil)
	criticalFailure := findHardCheck(adversarialChecks, "forbidden_content:系统提示词")
	if criticalFailure == nil || criticalFailure.Passed || !criticalFailure.Critical {
		t.Fatalf("adversarial fixture critical forbidden-content check = %#v, want critical failure", criticalFailure)
	}
	adversarialFields := evaluatorFixtureFields(t, adversarialCase, adversarialAnswer)
	if adversarialFields["forbidden_content"] != `["系统提示词","API key","内部推理"]` {
		t.Fatalf("critical forbidden content was not mapped to CozeLoop: %q", adversarialFields["forbidden_content"])
	}
}

func evaluatorFixtureFields(t *testing.T, currentCase EvalCase, answer string) map[string]string {
	t.Helper()
	runID := "asset-fixture-run"
	result := CaseResult{
		RunID:       runID,
		CaseID:      currentCase.ID,
		CaseVersion: currentCase.Version,
		RepeatIndex: 1,
		Status:      "failed",
		Answer:      answer,
	}
	items, err := BuildDatasetItems([]EvalCase{currentCase}, RunReport{RunID: runID, Cases: []CaseResult{result}})
	if err != nil {
		t.Fatalf("BuildDatasetItems() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("BuildDatasetItems() returned %d items, want 1", len(items))
	}
	return items[0].Fields
}

func TestRepositoryPromptEvaluatorAssets(t *testing.T) {
	manifest, err := LoadEvaluatorManifestFile(repositoryPath(t, "evals", "cozeloop", "evaluators.v1.json"))
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}
	definitions := make(map[string]EvaluatorDefinition, len(manifest.Evaluators))
	for _, definition := range manifest.Evaluators {
		definitions[definition.Key] = definition
	}

	variables := []string{"input_query", "interview_context", "actual_output", "required_facts"}
	seenHashes := make(map[string]string)
	for _, key := range []string{
		"answer-faithfulness",
		"instruction-following",
		"answer-completeness",
		"answer-actionability",
	} {
		definition, exists := definitions[key]
		if !exists {
			t.Fatalf("manifest is missing Prompt evaluator %q", key)
		}
		if definition.Type != EvaluatorTypePrompt || definition.Version != "1.0.0" || definition.ContentHash == "" {
			t.Errorf("evaluator %q has type %q, version %q, hash %q", key, definition.Type, definition.Version, definition.ContentHash)
		}
		if previous, exists := seenHashes[definition.ContentHash]; exists {
			t.Errorf("evaluators %q and %q unexpectedly share a content hash", previous, key)
		}
		seenHashes[definition.ContentHash] = key
		for _, variable := range variables {
			if !hasEvaluatorSchema(definition.InputSchemas, variable, "string", variable == "input_query" || variable == "actual_output") {
				t.Errorf("evaluator %q is missing the expected %q input schema", key, variable)
			}
			if !strings.Contains(definition.AssetContent, "{{"+variable+"}}") {
				t.Errorf("evaluator %q prompt is missing variable %q", key, variable)
			}
		}
		if !hasEvaluatorScoreRange(definition.OutputSchemas, 0, 4) || !hasEvaluatorSchema(definition.OutputSchemas, "reason", "string", true) {
			t.Errorf("evaluator %q must declare score range 0-4 and a required reason", key)
		}
		if !strings.Contains(definition.AssetContent, `"score"`) || !strings.Contains(definition.AssetContent, `"reason"`) {
			t.Errorf("evaluator %q prompt is missing the JSON score/reason output contract", key)
		}
		dimensions := map[string]string{
			"answer-faithfulness":   "忠实度",
			"instruction-following": "指令遵循",
			"answer-completeness":   "完整性",
			"answer-actionability":  "可执行性",
		}
		if !strings.Contains(definition.AssetContent, "本次只评估“"+dimensions[key]+"”") {
			t.Errorf("evaluator %q does not state its independent scoring dimension", key)
		}
	}
}

func hasEvaluatorSchema(schemas []EvaluatorFieldSchema, key, schemaType string, required bool) bool {
	for _, schema := range schemas {
		if schema.Key == key && schema.Type == schemaType && schema.Required == required {
			return true
		}
	}
	return false
}

func hasEvaluatorScoreRange(schemas []EvaluatorFieldSchema, minimum, maximum float64) bool {
	for _, schema := range schemas {
		if schema.Key == "score" && schema.Type == "number" && schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum == minimum && *schema.Maximum == maximum {
			return true
		}
	}
	return false
}

func repositoryPath(t *testing.T, parts ...string) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	return filepath.Join(append([]string{root}, parts...)...)
}

// Input mapping tests.
func TestBuildEvaluatorInputBatchMapsDeclaredFieldsAndPreservesOrder(t *testing.T) {
	manifest, err := LoadEvaluatorManifestFile(repositoryPath(t, "evals", "cozeloop", "evaluators.v1.json"))
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}
	codeDefinition := evaluatorDefinitionByKey(t, manifest, "answer-must-contain")
	promptDefinition := evaluatorDefinitionByKey(t, manifest, "answer-faithfulness")
	traceDefinition := evaluatorDefinitionByKey(t, manifest, "tool-trace-structure")

	firstCase := EvalCase{
		ID:      "case-first",
		Version: DatasetVersion,
		Input:   EvalInput{Query: "question first", InterviewContext: "context first"},
		AnswerExpectations: AnswerExpectations{
			MustContain:    []string{"expected fact"},
			MustNotContain: []string{"ordinary forbidden"},
			CriticalForbid: []string{"critical forbidden"},
			JudgeRubric:    "case rubric",
		},
	}
	failedCase := EvalCase{
		ID:      "case-infrastructure-failure",
		Version: DatasetVersion,
		Input:   EvalInput{Query: "must not be sent"},
	}
	lastCase := EvalCase{
		ID:      "case-last",
		Version: DatasetVersion,
		Input:   EvalInput{Query: "question last", InterviewContext: "context last"},
		AnswerExpectations: AnswerExpectations{
			MustContain: []string{"last fact"},
		},
	}
	runID := "run-evaluator-input"
	report := RunReport{
		RunID: runID,
		Cases: []CaseResult{
			{
				RunID:       runID,
				CaseID:      firstCase.ID,
				CaseVersion: DatasetVersion,
				RepeatIndex: 1,
				Status:      "passed",
				Answer:      "answer first",
				ToolTrace:   []ToolTrace{{Name: "search_question_memory", Arguments: map[string]any{"query": "first"}, Error: "raw private answer"}},
			},
			{
				RunID:                 runID,
				CaseID:                failedCase.ID,
				CaseVersion:           DatasetVersion,
				RepeatIndex:           1,
				Status:                "error",
				Answer:                "private infrastructure output",
				Error:                 "raw infrastructure error",
				InfrastructureFailure: true,
			},
			{
				RunID:       runID,
				CaseID:      lastCase.ID,
				CaseVersion: DatasetVersion,
				RepeatIndex: 2,
				Status:      "failed",
				Answer:      "answer last",
			},
		},
	}
	cases := []EvalCase{firstCase, failedCase, lastCase}

	codeInputs, err := BuildEvaluatorInputBatch(codeDefinition, cases, report)
	if err != nil {
		t.Fatalf("BuildEvaluatorInputBatch(code) error = %v", err)
	}
	if len(codeInputs) != 2 {
		t.Fatalf("code inputs = %d, want 2 after filtering infrastructure failure", len(codeInputs))
	}
	if got := codeInputs[0].EvaluateTargetOutputFields["actual_output"].Text; got != "answer first" {
		t.Fatalf("first code target output = %q", got)
	}
	if got := codeInputs[0].EvaluateTargetOutputFields["actual_output"].ContentType; got != EvaluatorContentTypeText {
		t.Fatalf("target output content type = %q", got)
	}
	if len(codeInputs[0].EvaluateDatasetFields) != 1 || len(codeInputs[0].EvaluateTargetOutputFields) != 1 {
		t.Fatalf("Code evaluator received undeclared fields: dataset %d, target %d", len(codeInputs[0].EvaluateDatasetFields), len(codeInputs[0].EvaluateTargetOutputFields))
	}
	if got := codeInputs[1].EvaluateTargetOutputFields["actual_output"].Text; got != "answer last" {
		t.Fatalf("second code target output = %q", got)
	}
	if got := codeInputs[0].EvaluateDatasetFields["required_facts"].Text; got != `["expected fact"]` {
		t.Fatalf("mapped required_facts = %q", got)
	}
	if len(codeInputs[0].Ext) != 0 {
		t.Fatalf("evaluator input must not include local run metadata: %#v", codeInputs[0].Ext)
	}
	if len(codeInputs[0].InputFields) != 0 {
		t.Fatalf("Code evaluator unexpectedly received input_fields: %#v", codeInputs[0].InputFields)
	}
	if strings.Contains(stringifyEvaluatorInput(codeInputs[0]), "raw private answer") || strings.Contains(stringifyEvaluatorInput(codeInputs[0]), "private infrastructure output") {
		t.Fatal("evaluator input contains an unredacted tool error or infrastructure-failure sample")
	}

	promptInputs, err := BuildEvaluatorInputBatch(promptDefinition, cases, report)
	if err != nil {
		t.Fatalf("BuildEvaluatorInputBatch(prompt) error = %v", err)
	}
	if len(promptInputs) != 2 {
		t.Fatalf("prompt inputs = %d, want 2", len(promptInputs))
	}
	prompt := promptInputs[0]
	if got := prompt.InputFields["input_query"].Text; got != "question first" {
		t.Fatalf("mapped prompt query = %q", got)
	}
	if got := prompt.InputFields["input_query"].ContentType; got != EvaluatorContentTypeText {
		t.Fatalf("prompt query content type = %q", got)
	}
	if got := prompt.InputFields["interview_context"].Text; got != "context first" {
		t.Fatalf("mapped prompt context = %q", got)
	}
	if got := prompt.InputFields["required_facts"].Text; got != `["expected fact"]` {
		t.Fatalf("mapped prompt reference facts = %q", got)
	}
	if got := prompt.InputFields["actual_output"].Text; got != "answer first" {
		t.Fatalf("mapped prompt actual output = %q", got)
	}
	if len(prompt.EvaluateDatasetFields) != 0 || len(prompt.EvaluateTargetOutputFields) != 0 {
		t.Fatalf("Prompt evaluator must receive variables only through input_fields: dataset %d, target %d", len(prompt.EvaluateDatasetFields), len(prompt.EvaluateTargetOutputFields))
	}

	traceInputs, err := BuildEvaluatorInputBatch(traceDefinition, cases, report)
	if err != nil {
		t.Fatalf("BuildEvaluatorInputBatch(tool trace) error = %v", err)
	}
	traceValue := traceInputs[0].EvaluateDatasetFields["tool_trace"].Text
	if !strings.Contains(traceValue, `"error":"execution_error"`) || strings.Contains(traceValue, "raw private answer") || strings.Contains(traceValue, `"query":"first"`) {
		t.Fatalf("Tool Trace error was not sanitized: %q", traceValue)
	}
}

func TestBuildEvaluatorInputBatchReturnsEmptyWhenAllSamplesHaveInfrastructureFailures(t *testing.T) {
	definition := EvaluatorDefinition{
		Key:           "answer-nonempty",
		Name:          "non-empty",
		Type:          EvaluatorTypeCode,
		Version:       "1.0.0",
		AssetFile:     "code.py",
		InputSchemas:  []EvaluatorFieldSchema{{Key: "actual_output", Type: "string", Required: true}},
		OutputSchemas: []EvaluatorFieldSchema{{Key: "score", Type: "number", Required: true}},
	}
	caseData := EvalCase{ID: "failed", Version: DatasetVersion, Input: EvalInput{Query: "q"}}
	runID := "run-only-failure"
	inputs, err := BuildEvaluatorInputBatch(definition, []EvalCase{caseData}, RunReport{
		RunID: runID,
		Cases: []CaseResult{{
			RunID: runID, CaseID: caseData.ID, CaseVersion: caseData.Version, RepeatIndex: 1,
			Status: "error", InfrastructureFailure: true,
		}},
	})
	if err != nil {
		t.Fatalf("BuildEvaluatorInputBatch() error = %v", err)
	}
	if len(inputs) != 0 {
		t.Fatalf("inputs = %d, want 0", len(inputs))
	}
}

func evaluatorDefinitionByKey(t *testing.T, manifest EvaluatorManifest, key string) EvaluatorDefinition {
	t.Helper()
	for _, definition := range manifest.Evaluators {
		if definition.Key == key {
			return definition
		}
	}
	t.Fatalf("manifest does not define %q", key)
	return EvaluatorDefinition{}
}

func stringifyEvaluatorInput(input EvaluatorInputData) string {
	encoded, _ := json.Marshal(input)
	return string(encoded)
}

// Manifest validation tests.
func TestLoadEvaluatorManifestFileLoadsAssetAndStableHash(t *testing.T) {
	directory := t.TempDir()
	manifestPath := writeEvaluatorManifest(t, directory, validEvaluatorManifest("assets/nonempty.py"), "def evaluate(actual_output):\n    return {\"score\": 1, \"reasoning\": \"non-empty\"}\n")

	first, err := LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}
	second, err := LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("second LoadEvaluatorManifestFile() error = %v", err)
	}
	if len(first.Evaluators) != 1 {
		t.Fatalf("loaded %d evaluators, want 1", len(first.Evaluators))
	}
	asset := first.Evaluators[0]
	if asset.AssetContent == "" {
		t.Fatal("loaded asset content is empty")
	}
	if !strings.HasPrefix(asset.ContentHash, "sha256:") {
		t.Fatalf("content hash = %q, want sha256 prefix", asset.ContentHash)
	}
	if asset.ContentHash != second.Evaluators[0].ContentHash {
		t.Fatalf("content hash changed between loads: %q != %q", asset.ContentHash, second.Evaluators[0].ContentHash)
	}

	if err := os.WriteFile(filepath.Join(directory, "assets", "nonempty.py"), []byte("# changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("load after changing asset: %v", err)
	}
	if changed.Evaluators[0].ContentHash == asset.ContentHash {
		t.Fatal("content hash did not change with asset content")
	}
}

func TestEvaluatorPlatformContentHashTracksOnlyPlatformContent(t *testing.T) {
	definition := validEvaluatorManifestValue().Evaluators[0]
	definition.AssetContent = `def exec_evaluation(turn): return EvalOutput(score=1, reason="ok")`
	initial, err := EvaluatorPlatformContentHash(definition)
	if err != nil {
		t.Fatalf("EvaluatorPlatformContentHash() error = %v", err)
	}

	metadataOnly := definition
	metadataOnly.Name = "renamed evaluator"
	metadataOnly.Version = "2.0.0"
	metadataOnly.Key = "renamed-evaluator"
	metadataHash, err := EvaluatorPlatformContentHash(metadataOnly)
	if err != nil {
		t.Fatalf("hash metadata-only change: %v", err)
	}
	if metadataHash != initial {
		t.Fatal("local name/key/version changed the platform content hash")
	}

	contentChanged := definition
	contentChanged.AssetContent += "\\n# change"
	contentHash, err := EvaluatorPlatformContentHash(contentChanged)
	if err != nil {
		t.Fatalf("hash asset change: %v", err)
	}
	if contentHash == initial {
		t.Fatal("asset change did not change the platform content hash")
	}

	schemaChanged := definition
	schemaChanged.OutputSchemas = append([]EvaluatorFieldSchema(nil), definition.OutputSchemas...)
	minimum := float64(0)
	schemaChanged.OutputSchemas[0].Minimum = &minimum
	schemaHash, err := EvaluatorPlatformContentHash(schemaChanged)
	if err != nil {
		t.Fatalf("hash schema change: %v", err)
	}
	if schemaHash == initial {
		t.Fatal("schema change did not change the platform content hash")
	}

	optionalField := definition
	optionalField.InputSchemas = append([]EvaluatorFieldSchema(nil), definition.InputSchemas...)
	optionalField.InputSchemas[0].Required = false
	optionalHash, err := EvaluatorPlatformContentHash(optionalField)
	if err != nil {
		t.Fatalf("hash requiredness change: %v", err)
	}
	if optionalHash == initial {
		t.Fatal("requiredness change did not change the platform content hash")
	}
}

func TestLoadEvaluatorManifestFileRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		directory := t.TempDir()
		manifest := strings.TrimSuffix(validEvaluatorManifest("assets/nonempty.py"), "}") + `,"unexpected":true}`
		path := writeEvaluatorManifest(t, directory, manifest, "code")
		if _, err := LoadEvaluatorManifestFile(path); err == nil {
			t.Fatal("LoadEvaluatorManifestFile() error = nil, want unknown field error")
		}
	})

	t.Run("trailing value", func(t *testing.T) {
		directory := t.TempDir()
		path := writeEvaluatorManifest(t, directory, validEvaluatorManifest("assets/nonempty.py")+` {}`, "code")
		if _, err := LoadEvaluatorManifestFile(path); err == nil {
			t.Fatal("LoadEvaluatorManifestFile() error = nil, want trailing JSON error")
		}
	})
}

func TestEvaluatorManifestValidateRejectsDuplicateKeysAndInvalidSchemas(t *testing.T) {
	tests := map[string]func(*EvaluatorManifest){
		"duplicate key": func(manifest *EvaluatorManifest) {
			duplicate := manifest.Evaluators[0]
			duplicate.Name = "another name"
			manifest.Evaluators = append(manifest.Evaluators, duplicate)
		},
		"duplicate name": func(manifest *EvaluatorManifest) {
			duplicate := manifest.Evaluators[0]
			duplicate.Key = "answer-nonempty-copy"
			manifest.Evaluators = append(manifest.Evaluators, duplicate)
		},
		"duplicate input schema key": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].InputSchemas = append(manifest.Evaluators[0].InputSchemas, manifest.Evaluators[0].InputSchemas[0])
		},
		"unsupported input schema type": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].InputSchemas[0].Type = "binary"
		},
		"non-numeric range": func(manifest *EvaluatorManifest) {
			minimum := float64(0)
			manifest.Evaluators[0].InputSchemas[0].Minimum = &minimum
		},
		"reversed output range": func(manifest *EvaluatorManifest) {
			minimum, maximum := float64(4), float64(0)
			manifest.Evaluators[0].OutputSchemas[0].Minimum = &minimum
			manifest.Evaluators[0].OutputSchemas[0].Maximum = &maximum
		},
		"padded input schema key": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].InputSchemas[0].Key = " actual_output "
		},
		"local run metadata is not an evaluator input": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].InputSchemas[0].Key = "run_id"
		},
		"missing version": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].Version = " "
		},
		"padded version": func(manifest *EvaluatorManifest) {
			manifest.Evaluators[0].Version = " 1.0.0 "
		},
		"unsupported manifest version": func(manifest *EvaluatorManifest) {
			manifest.Version++
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := validEvaluatorManifestValue()
			mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestLoadEvaluatorManifestFileRejectsAssetPathEscape(t *testing.T) {
	directory := t.TempDir()
	outsidePath := filepath.Join(directory, "outside.py")
	if err := os.WriteFile(outsidePath, []byte("not in the manifest directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestDirectory := filepath.Join(directory, "manifest")
	if err := os.MkdirAll(manifestDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := writeEvaluatorManifest(t, manifestDirectory, validEvaluatorManifest("../outside.py"), "unused")
	if _, err := LoadEvaluatorManifestFile(path); err == nil {
		t.Fatal("LoadEvaluatorManifestFile() error = nil, want asset path escape error")
	}
}

func TestEvaluatorManifestRoundTrips(t *testing.T) {
	manifest := validEvaluatorManifestValue()
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded EvaluatorManifest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-tripped manifest is invalid: %v", err)
	}
}

func writeEvaluatorManifest(t *testing.T, directory, manifest, asset string) string {
	t.Helper()
	assetPath := filepath.Join(directory, "assets", "nonempty.py")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte(asset), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(directory, "evaluators.json")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

func validEvaluatorManifest(version string) string {
	return `{"version":1,"evaluators":[{"key":"answer-nonempty","name":"Interview Agent / Code / Answer Nonempty","type":"code","version":"1.0.0","assetFile":"` + version + `","inputSchemas":[{"key":"actual_output","type":"string","required":true}],"outputSchemas":[{"key":"score","type":"number","required":true},{"key":"reasoning","type":"string","required":true}]}]}`
}

func validEvaluatorManifestValue() EvaluatorManifest {
	return EvaluatorManifest{
		Version: EvaluatorManifestVersion,
		Evaluators: []EvaluatorDefinition{{
			Key:       "answer-nonempty",
			Name:      "Interview Agent / Code / Answer Nonempty",
			Type:      EvaluatorTypeCode,
			Version:   "1.0.0",
			AssetFile: "assets/nonempty.py",
			InputSchemas: []EvaluatorFieldSchema{{
				Key:      "actual_output",
				Type:     "string",
				Required: true,
			}},
			OutputSchemas: []EvaluatorFieldSchema{
				{Key: "score", Type: "number", Required: true},
				{Key: "reasoning", Type: "string", Required: true},
			},
		}},
	}
}
