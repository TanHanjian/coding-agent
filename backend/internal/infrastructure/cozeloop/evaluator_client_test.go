package cozeloop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"interview-memory-agent/backend/internal/eval"
	"interview-memory-agent/backend/internal/infrastructure/config"
)

func loadCozeLoopTestEvaluator(t *testing.T, key string) eval.EvaluatorDefinition {
	t.Helper()
	manifestPath := filepath.Join("..", "..", "..", "..", "evals", "cozeloop", "evaluators.v1.json")
	manifest, err := eval.LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}
	for _, definition := range manifest.Evaluators {
		if definition.Key == key {
			return definition
		}
	}
	t.Fatalf("evaluator %q was not found", key)
	return eval.EvaluatorDefinition{}
}

func newEvaluatorTestClient(t *testing.T, serverURL string) *OpenAPIEvaluationClient {
	t.Helper()
	client, err := newOpenAPIEvaluationClient(serverURL, "12345", "test-token", http.DefaultClient)
	if err != nil {
		t.Fatalf("newOpenAPIEvaluationClient() error = %v", err)
	}
	return client
}

func codeEvaluatorTestInput(output string) eval.EvaluatorInputData {
	return eval.EvaluatorInputData{
		EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{
			"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: output},
		},
	}
}

func newAuthorizedEvaluatorTestClient(t *testing.T, serverURL string) *OpenAPIEvaluationClient {
	t.Helper()
	client := newEvaluatorTestClient(t, serverURL)
	client.contentUploadEnabled = true
	client.consentPath = filepath.Join(t.TempDir(), "consent.json")
	if err := GrantContentConsent(client.consentPath, "12345", serverURL, []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
		t.Fatalf("GrantContentConsent() error = %v", err)
	}
	return client
}

func decodeEvaluatorTestBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	defer request.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Errorf("decode request body: %v", err)
	}
	return body
}

func writeEvaluatorTestEnvelope(t *testing.T, writer http.ResponseWriter, status int, data any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if status < 200 || status >= 300 {
		_, _ = fmt.Fprint(writer, `{"message":"secret response detail"}`)
		return
	}
	if err := json.NewEncoder(writer).Encode(map[string]any{"code": 0, "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestOpenAPIEvaluatorClientUsesPublicIDLRoutesAndDTOs(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	content, err := evaluatorContentPayload(definition)
	if err != nil {
		t.Fatalf("evaluatorContentPayload() error = %v", err)
	}
	var sawList, sawCreate, sawUpdate, sawVersions, sawSubmit bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("cozeloop-workspace-id") != "12345" || request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing workspace/auth headers")
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/evaluation/v1/evaluators/list":
			body := decodeEvaluatorTestBody(t, request)
			if body["workspace_id"] != "12345" || body["search_name"] != definition.Name || body["with_version"] != true || body["page_number"] != float64(1) || body["page_size"] != float64(evaluatorPageSize) {
				t.Errorf("unexpected evaluator list body: %#v", body)
			}
			types, ok := body["evaluator_type"].([]any)
			if !ok || len(types) != 1 || types[0] != float64(cozeEvaluatorTypeCode) {
				t.Errorf("unexpected evaluator_type filter: %#v", body["evaluator_type"])
			}
			sawList = true
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{
				"evaluators": []any{map[string]any{"evaluator_id": "101", "name": definition.Name, "evaluator_type": cozeEvaluatorTypeCode}},
				"total":      1,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/evaluation/v1/evaluators":
			body := decodeEvaluatorTestBody(t, request)
			remote := body["evaluator"].(map[string]any)
			if body["workspace_id"] != "12345" || remote["name"] != definition.Name || remote["description"] != definition.Description || remote["evaluator_type"] != float64(cozeEvaluatorTypeCode) {
				t.Errorf("unexpected create evaluator body: %#v", body)
			}
			sawCreate = true
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_id": "101"})
		case request.Method == http.MethodPatch && request.URL.Path == "/api/evaluation/v1/evaluators/101/update_draft":
			body := decodeEvaluatorTestBody(t, request)
			remoteContent := body["evaluator_content"].(map[string]any)
			code := remoteContent["code_evaluator"].(map[string]any)
			if body["workspace_id"] != "12345" || body["evaluator_type"] != float64(cozeEvaluatorTypeCode) || code["language_type"] != "Python" || code["code_content"] != definition.AssetContent {
				t.Errorf("unexpected update draft body: %#v", body)
			}
			schemas := remoteContent["output_schemas"].([]any)
			firstSchema := schemas[0].(map[string]any)
			var scoreSchema map[string]any
			if err := json.Unmarshal([]byte(firstSchema["json_schema"].(string)), &scoreSchema); err != nil {
				t.Errorf("decode output json_schema: %v", err)
			}
			if scoreSchema["type"] != "number" || scoreSchema["minimum"] != float64(0) || scoreSchema["maximum"] != float64(1) {
				t.Errorf("unexpected output schema: %#v", scoreSchema)
			}
			sawUpdate = true
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator": map[string]any{"evaluator_id": "101", "name": definition.Name}})
		case request.Method == http.MethodPost && request.URL.Path == "/api/evaluation/v1/evaluators/101/versions/list":
			body := decodeEvaluatorTestBody(t, request)
			if body["workspace_id"] != "12345" || body["page_number"] != float64(1) || body["page_size"] != float64(evaluatorPageSize) {
				t.Errorf("unexpected evaluator versions body: %#v", body)
			}
			sawVersions = true
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{
				"evaluator_versions": []any{map[string]any{"id": "501", "version": definition.Version, "evaluator_content": content}},
				"total":              1,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/evaluation/v1/evaluators/101/submit_version":
			body := decodeEvaluatorTestBody(t, request)
			if body["workspace_id"] != "12345" || body["version"] != definition.Version || body["description"] != definition.Description {
				t.Errorf("unexpected submit version body: %#v", body)
			}
			sawSubmit = true
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator": map[string]any{"current_version": map[string]any{"id": "501", "version": definition.Version}}})
		default:
			t.Errorf("unexpected evaluator request: %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newEvaluatorTestClient(t, server.URL)

	listed, err := client.ListEvaluators(context.Background(), eval.EvaluatorListFilter{Name: definition.Name, Type: definition.Type})
	if err != nil || len(listed) != 1 || listed[0].ID != "101" {
		t.Fatalf("ListEvaluators() = %#v, %v", listed, err)
	}
	created, err := client.CreateEvaluator(context.Background(), definition)
	if err != nil || created.ID != "101" {
		t.Fatalf("CreateEvaluator() = %#v, %v", created, err)
	}
	updated, err := client.UpdateEvaluatorDraft(context.Background(), created.ID, definition)
	if err != nil || updated.ID != created.ID {
		t.Fatalf("UpdateEvaluatorDraft() = %#v, %v", updated, err)
	}
	versions, err := client.ListEvaluatorVersions(context.Background(), created.ID)
	if err != nil || len(versions) != 1 || versions[0].Version != definition.Version {
		t.Fatalf("ListEvaluatorVersions() = %#v, %v", versions, err)
	}
	expectedHash, err := eval.EvaluatorPlatformContentHash(definition)
	if err != nil || versions[0].ContentHash != expectedHash {
		t.Fatalf("platform content hash = %q, expected %q, error %v", versions[0].ContentHash, expectedHash, err)
	}
	submitted, err := client.SubmitEvaluatorVersion(context.Background(), created.ID, definition.Version, definition.Description)
	if err != nil || submitted.ID != "501" || submitted.Version != definition.Version {
		t.Fatalf("SubmitEvaluatorVersion() = %#v, %v", submitted, err)
	}
	if !sawList || !sawCreate || !sawUpdate || !sawVersions || !sawSubmit {
		t.Fatalf("missing route coverage: list=%v create=%v update=%v versions=%v submit=%v", sawList, sawCreate, sawUpdate, sawVersions, sawSubmit)
	}
}

func TestNormalizeEvaluatorInputOnlyTransmitsDeclaredFields(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "..", "..", "evals", "cozeloop", "evaluators.v1.json")
	manifest, err := eval.LoadEvaluatorManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("LoadEvaluatorManifestFile() error = %v", err)
	}
	code := loadCozeLoopTestEvaluator(t, "answer-must-contain")
	codeInput := eval.EvaluatorInputData{
		EvaluateDatasetFields: map[string]eval.EvaluatorFieldContent{
			"required_facts": {ContentType: eval.EvaluatorContentTypeText, Text: "fact"},
		},
		EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{
			"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: "answer"},
		},
		Ext: map[string]string{"credential": "must-not-be-forwarded"},
	}
	normalized, err := normalizeEvaluatorInput(code, codeInput)
	if err != nil {
		t.Fatalf("normalize Code input: %v", err)
	}
	if len(normalized.EvaluateDatasetFields) != 1 || len(normalized.EvaluateTargetOutputFields) != 1 || len(normalized.Ext) != 0 || len(normalized.InputFields) != 0 {
		t.Fatalf("unexpected normalized Code input: %#v", normalized)
	}
	if strings.Contains(stringifyEvaluatorTestInput(normalized), "must-not-be-forwarded") {
		t.Fatal("normalization forwarded undeclared extension data")
	}
	codeInput.EvaluateDatasetFields["undeclared"] = eval.EvaluatorFieldContent{ContentType: eval.EvaluatorContentTypeText, Text: "extra"}
	if _, err := normalizeEvaluatorInput(code, codeInput); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("normalize Code input with an extra field error = %v", err)
	}

	var prompt eval.EvaluatorDefinition
	for _, definition := range manifest.Evaluators {
		if definition.Key == "answer-faithfulness" {
			prompt = definition
			break
		}
	}
	promptInput := eval.EvaluatorInputData{InputFields: map[string]eval.EvaluatorFieldContent{
		"input_query":   {ContentType: eval.EvaluatorContentTypeText, Text: "question"},
		"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: "answer"},
	}}
	normalized, err = normalizeEvaluatorInput(prompt, promptInput)
	if err != nil {
		t.Fatalf("normalize Prompt input: %v", err)
	}
	if len(normalized.InputFields) != len(prompt.InputSchemas) || len(normalized.EvaluateDatasetFields) != 0 || len(normalized.EvaluateTargetOutputFields) != 0 {
		t.Fatalf("unexpected normalized Prompt input: %#v", normalized)
	}
	if normalized.InputFields["interview_context"].Text != "" || normalized.InputFields["interview_context"].ContentType != eval.EvaluatorContentTypeText {
		t.Fatalf("optional prompt field default = %#v", normalized.InputFields["interview_context"])
	}
	promptInput.EvaluateDatasetFields = map[string]eval.EvaluatorFieldContent{"not_declared": {ContentType: eval.EvaluatorContentTypeText, Text: "extra"}}
	if _, err := normalizeEvaluatorInput(prompt, promptInput); err == nil || !strings.Contains(err.Error(), "input_fields") {
		t.Fatalf("normalize Prompt input with wrong field group error = %v", err)
	}
}

func stringifyEvaluatorTestInput(input eval.EvaluatorInputData) string {
	encoded, _ := json.Marshal(input)
	return string(encoded)
}

func TestPromptEvaluatorContentUsesPromptIDLShape(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-faithfulness")
	content, err := evaluatorContentPayload(definition)
	if err != nil {
		t.Fatalf("evaluatorContentPayload() error = %v", err)
	}
	prompt := content["prompt_evaluator"].(map[string]any)
	messages := prompt["message_list"].([]any)
	message := messages[0].(map[string]any)
	messageContent := message["content"].(map[string]any)
	if message["role"] != cozeMessageRoleSystem || messageContent["content_type"] != eval.EvaluatorContentTypeText || messageContent["text"] != definition.AssetContent || prompt["prompt_source_type"] != cozePromptSourceCustom {
		t.Fatalf("unexpected prompt evaluator payload: %#v", prompt)
	}
	inputSchemas := content["input_schemas"].([]map[string]any)
	var foundOptional, foundRequired bool
	for _, schema := range inputSchemas {
		switch schema["key"] {
		case "interview_context":
			defaultValue, ok := schema["default_value"].(map[string]any)
			foundOptional = ok && defaultValue["content_type"] == eval.EvaluatorContentTypeText && defaultValue["text"] == ""
		case "input_query":
			_, foundRequired = schema["default_value"]
		}
	}
	if !foundOptional || foundRequired {
		t.Fatalf("optional schema default mapping mismatch: %#v", inputSchemas)
	}
	if content["receive_chat_history"] != false {
		t.Fatalf("receive_chat_history must remain disabled: %#v", content["receive_chat_history"])
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("json.Marshal(content) error = %v", err)
	}
	remoteHash, err := evaluatorContentHashFromPayload(encoded)
	if err != nil {
		t.Fatalf("evaluatorContentHashFromPayload() error = %v", err)
	}
	localHash, err := eval.EvaluatorPlatformContentHash(definition)
	if err != nil || remoteHash != localHash {
		t.Fatalf("prompt platform hash = %q, expected %q, error %v", remoteHash, localHash, err)
	}
}

func TestEnsureEvaluatorResourceHandlesExactTypeConflictAndCreateReadback(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	t.Run("same name with different type conflicts", func(t *testing.T) {
		created := false
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/evaluation/v1/evaluators/list" {
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluators": []any{map[string]any{"evaluator_id": "101", "name": definition.Name, "evaluator_type": cozeEvaluatorTypePrompt}}, "total": 1})
				return
			}
			created = true
			t.Errorf("CreateEvaluator must not run on a type conflict")
			http.NotFound(writer, request)
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		if _, err := eval.EnsureEvaluatorResource(context.Background(), client, definition); err == nil || !strings.Contains(err.Error(), "conflicts") {
			t.Fatalf("EnsureEvaluatorResource() error = %v, want conflict", err)
		}
		if created {
			t.Fatal("unexpected evaluator create request")
		}
	})

	t.Run("create response loss reuses exact readback", func(t *testing.T) {
		listCalls := 0
		createCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/evaluation/v1/evaluators/list":
				listCalls++
				if listCalls == 1 {
					writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluators": []any{}, "total": 0})
					return
				}
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluators": []any{map[string]any{"evaluator_id": "101", "name": definition.Name, "evaluator_type": cozeEvaluatorTypeCode}}, "total": 1})
			case "/api/evaluation/v1/evaluators":
				createCalls++
				writeEvaluatorTestEnvelope(t, writer, http.StatusConflict, nil)
			default:
				t.Errorf("unexpected route %s", request.URL.Path)
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		got, err := eval.EnsureEvaluatorResource(context.Background(), client, definition)
		if err != nil || got.ID != "101" || got.Type != definition.Type {
			t.Fatalf("EnsureEvaluatorResource() = %#v, %v", got, err)
		}
		if listCalls != 2 || createCalls != 1 {
			t.Fatalf("calls: list=%d create=%d, want list=2 create=1", listCalls, createCalls)
		}
	})
}

func TestEnsureEvaluatorVersionReuseConflictAndRetryReadback(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	localContent, err := evaluatorContentPayload(definition)
	if err != nil {
		t.Fatalf("evaluatorContentPayload() error = %v", err)
	}
	changedDefinition := definition
	changedDefinition.AssetContent += "\n# remote change"
	changedContent, err := evaluatorContentPayload(changedDefinition)
	if err != nil {
		t.Fatalf("changed evaluatorContentPayload() error = %v", err)
	}

	t.Run("fixed version with matching hash is reused", func(t *testing.T) {
		submitCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/evaluation/v1/evaluators/101/versions/list" {
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{map[string]any{"id": "501", "version": definition.Version, "evaluator_content": localContent}}, "total": 1})
				return
			}
			submitCalls++
			t.Errorf("matching fixed version must not be submitted again")
			http.NotFound(writer, request)
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		got, err := eval.EnsureEvaluatorVersion(context.Background(), client, "101", definition)
		if err != nil || got.ID != "501" || got.ContentHash == "" {
			t.Fatalf("EnsureEvaluatorVersion() = %#v, %v", got, err)
		}
		if submitCalls != 0 {
			t.Fatalf("submit calls = %d, want 0", submitCalls)
		}
	})

	t.Run("fixed version with different hash conflicts", func(t *testing.T) {
		submitCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/api/evaluation/v1/evaluators/101/versions/list" {
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{map[string]any{"id": "501", "version": definition.Version, "evaluator_content": changedContent}}, "total": 1})
				return
			}
			submitCalls++
			http.NotFound(writer, request)
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		if _, err := eval.EnsureEvaluatorVersion(context.Background(), client, "101", definition); err == nil || !strings.Contains(err.Error(), "conflicts") {
			t.Fatalf("EnsureEvaluatorVersion() error = %v, want content conflict", err)
		}
		if submitCalls != 0 {
			t.Fatalf("submit calls = %d, want 0", submitCalls)
		}
	})

	t.Run("submit failure is resolved by exact readback", func(t *testing.T) {
		listCalls := 0
		submitCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/evaluation/v1/evaluators/101/versions/list":
				listCalls++
				if listCalls == 1 {
					writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{}, "total": 0})
					return
				}
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{map[string]any{"id": "501", "version": definition.Version, "evaluator_content": localContent}}, "total": 1})
			case "/api/evaluation/v1/evaluators/101/submit_version":
				submitCalls++
				writeEvaluatorTestEnvelope(t, writer, http.StatusInternalServerError, nil)
			default:
				t.Errorf("unexpected route %s", request.URL.Path)
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		got, err := eval.EnsureEvaluatorVersion(context.Background(), client, "101", definition)
		if err != nil || got.ID != "501" {
			t.Fatalf("EnsureEvaluatorVersion() = %#v, %v", got, err)
		}
		if listCalls != 2 || submitCalls != 1 {
			t.Fatalf("calls: list=%d submit=%d, want list=2 submit=1", listCalls, submitCalls)
		}
	})

	t.Run("successful submit is accepted after content readback", func(t *testing.T) {
		listCalls := 0
		submitCalls := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/api/evaluation/v1/evaluators/101/versions/list":
				listCalls++
				if listCalls == 1 {
					writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{}, "total": 0})
					return
				}
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_versions": []any{map[string]any{"id": "501", "version": definition.Version, "evaluator_content": localContent}}, "total": 1})
			case "/api/evaluation/v1/evaluators/101/submit_version":
				submitCalls++
				writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator": map[string]any{"current_version": map[string]any{"id": "501", "version": definition.Version}}})
			default:
				t.Errorf("unexpected route %s", request.URL.Path)
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		client := newEvaluatorTestClient(t, server.URL)
		got, err := eval.EnsureEvaluatorVersion(context.Background(), client, "101", definition)
		if err != nil || got.ID != "501" || got.ContentHash == "" {
			t.Fatalf("EnsureEvaluatorVersion() = %#v, %v", got, err)
		}
		if listCalls != 2 || submitCalls != 1 {
			t.Fatalf("calls: list=%d submit=%d, want list=2 submit=1", listCalls, submitCalls)
		}
	})
}

func TestOpenAPIEvaluatorValidateAndBatchDebugUseIDLAndSanitizeResults(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	var validateCalls, batchCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/evaluation/v1/evaluators/validate":
			validateCalls++
			body := decodeEvaluatorTestBody(t, request)
			if request.Method != http.MethodPost || body["workspace_id"] != "12345" || body["evaluator_type"] != float64(cozeEvaluatorTypeCode) {
				t.Errorf("unexpected validate request: %s %#v", request.Method, body)
			}
			input := body["input_data"].(map[string]any)
			outputs := input["evaluate_target_output_fields"].(map[string]any)
			actualOutput := outputs["actual_output"].(map[string]any)
			if actualOutput["content_type"] != eval.EvaluatorContentTypeText || actualOutput["text"] != "candidate answer" {
				t.Errorf("unexpected validate input_data: %#v", input)
			}
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{
				"valid":                 true,
				"error_message":         "sensitive platform detail",
				"evaluator_output_data": map[string]any{"evaluator_result": map[string]any{"score": 0, "reasoning": "empty answer"}},
			})
		case "/api/evaluation/v1/evaluators/batch_debug":
			batchCalls++
			body := decodeEvaluatorTestBody(t, request)
			inputs := body["input_data"].([]any)
			if body["workspace_id"] != "12345" || body["evaluator_type"] != float64(cozeEvaluatorTypeCode) || len(inputs) != 2 {
				t.Errorf("unexpected batch debug body: %#v", body)
			}
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluator_output_data": []any{
				map[string]any{"evaluator_result": map[string]any{"score": 1, "reasoning": "non-empty"}},
				map[string]any{"evaluator_run_error": map[string]any{"code": 9001, "message": "candidate data must not leak"}},
			}})
		default:
			t.Errorf("unexpected evaluator debug route: %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newAuthorizedEvaluatorTestClient(t, server.URL)
	inputs := []eval.EvaluatorInputData{
		{EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: "candidate answer"}}},
		{EvaluateTargetOutputFields: map[string]eval.EvaluatorFieldContent{"actual_output": {ContentType: eval.EvaluatorContentTypeText, Text: ""}}},
	}
	validated, err := client.ValidateEvaluator(context.Background(), definition, inputs[0])
	if err != nil || !validated.Valid || validated.Result == nil || validated.Result.Score == nil || *validated.Result.Score != 0 || validated.Result.Reason != "empty answer" {
		t.Fatalf("ValidateEvaluator() = %#v, %v", validated, err)
	}
	debugged, err := client.BatchDebugEvaluator(context.Background(), definition, inputs)
	if err != nil || len(debugged) != 2 {
		t.Fatalf("BatchDebugEvaluator() = %#v, %v", debugged, err)
	}
	if debugged[0].InputIndex != 0 || debugged[0].Result.Score == nil || *debugged[0].Result.Score != 1 || debugged[0].Result.Reason != "non-empty" {
		t.Fatalf("first batch result = %#v", debugged[0])
	}
	if debugged[1].InputIndex != 1 || debugged[1].Result.Status != "fail" || debugged[1].Result.ErrorCategory != "evaluator_error" || strings.Contains(debugged[1].Result.ErrorCategory, "candidate data") {
		t.Fatalf("second batch result = %#v", debugged[1])
	}
	if validateCalls != 1 || batchCalls != 1 {
		t.Fatalf("validate calls=%d batch calls=%d, want one each", validateCalls, batchCalls)
	}
}

func TestParseEvaluatorExecutionResultRejectsIncompleteSuccess(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"evaluator_result":{}}`,
		`{"evaluator_result":{"score":1}}`,
		`{"evaluator_result":{"reasoning":"missing score"}}`,
	} {
		if result, err := parseEvaluatorExecutionResult(json.RawMessage(raw)); err == nil || result != nil {
			t.Errorf("parseEvaluatorExecutionResult(%s) = %#v, %v; want incomplete-result error", raw, result, err)
		}
	}
	failed, err := parseEvaluatorExecutionResult(json.RawMessage(`{"evaluator_run_error":{"code":1,"message":"private data"}}`))
	if err != nil || failed == nil || failed.Status != "fail" || failed.ErrorCategory != "evaluator_error" || failed.Reason != "" {
		t.Fatalf("parse platform run error = %#v, %v", failed, err)
	}
	if err := validateEvaluatorExecutionResult(eval.EvaluatorDefinition{OutputSchemas: []eval.EvaluatorFieldSchema{{Key: "score", Minimum: floatPointer(0), Maximum: floatPointer(1)}}}, &eval.EvaluatorExecutionResult{Status: "success", Score: floatPointer(2), Reason: "out of range"}); err == nil {
		t.Fatal("out-of-range evaluator score was accepted")
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestOpenAPIEvaluatorValidateRejectsMissingOutputForValidInput(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"valid": true})
	}))
	defer server.Close()
	client := newAuthorizedEvaluatorTestClient(t, server.URL)
	if _, err := client.ValidateEvaluator(context.Background(), definition, codeEvaluatorTestInput("candidate")); err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("ValidateEvaluator() without output error = %v", err)
	}
}

func TestNewEvaluatorClientSeparatesMetadataFromContentAuthorization(t *testing.T) {
	var listCalls, contentCalls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/evaluation/v1/evaluators/list" {
			listCalls++
			writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{"evaluators": []any{}, "total": 0})
			return
		}
		contentCalls++
		http.NotFound(writer, request)
	}))
	defer server.Close()
	platform, err := NewEvaluatorClient(config.CozeLoopConfig{
		Enabled:           true,
		EvaluationEnabled: true,
		APIBaseURL:        server.URL,
		WorkspaceID:       "12345",
		APIToken:          "test-token",
		ConsentPath:       filepath.Join(t.TempDir(), "missing-consent.json"),
	})
	if err != nil {
		t.Fatalf("NewEvaluatorClient() error = %v", err)
	}
	client, ok := platform.(*OpenAPIEvaluationClient)
	if !ok || client.httpClient.Timeout != evaluatorRequestTimeout {
		t.Fatalf("evaluator client timeout = %v, want %v", func() time.Duration {
			if !ok {
				return 0
			}
			return client.httpClient.Timeout
		}(), evaluatorRequestTimeout)
	}
	if _, err := platform.ListEvaluators(context.Background(), eval.EvaluatorListFilter{}); err != nil {
		t.Fatalf("ListEvaluators() without case-content consent: %v", err)
	}
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	if _, err := platform.ValidateEvaluator(context.Background(), definition, codeEvaluatorTestInput("candidate")); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("ValidateEvaluator() with upload switch off error = %v", err)
	}
	if listCalls != 1 || contentCalls != 0 {
		t.Fatalf("metadata calls=%d content calls=%d, want 1 and 0", listCalls, contentCalls)
	}
}

func TestEvaluatorContentRequestsRequireCurrentConsent(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		writeEvaluatorTestEnvelope(t, writer, http.StatusOK, map[string]any{
			"valid":                 true,
			"evaluator_output_data": map[string]any{"evaluator_result": map[string]any{"score": 1, "reasoning": "ok"}},
		})
	}))
	defer server.Close()
	client := newEvaluatorTestClient(t, server.URL)
	input := codeEvaluatorTestInput("candidate response")

	if _, err := client.ValidateEvaluator(context.Background(), definition, input); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("ValidateEvaluator() without upload switch error = %v", err)
	}
	client.contentUploadEnabled = true
	client.consentPath = filepath.Join(t.TempDir(), "consent.json")
	if _, err := client.ValidateEvaluator(context.Background(), definition, input); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("ValidateEvaluator() without grant error = %v", err)
	}
	if err := GrantContentConsent(client.consentPath, "12345", server.URL, []string{ConsentScopeTraceContent}, time.Now()); err != nil {
		t.Fatalf("GrantContentConsent(trace only) error = %v", err)
	}
	if _, err := client.ValidateEvaluator(context.Background(), definition, input); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("ValidateEvaluator() with wrong scope error = %v", err)
	}
	tooManyInputs := make([]eval.EvaluatorInputData, maxEvaluationBatchSize+1)
	if _, err := client.BatchDebugEvaluator(context.Background(), definition, tooManyInputs); err == nil || !strings.Contains(err.Error(), "exceeds the limit") {
		t.Fatalf("BatchDebugEvaluator() over limit error = %v", err)
	}
	if requestCount != 0 {
		t.Fatalf("request count without authorization = %d, want 0", requestCount)
	}
	if err := GrantContentConsent(client.consentPath, "12345", server.URL, []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
		t.Fatalf("GrantContentConsent() error = %v", err)
	}
	result, err := client.ValidateEvaluator(context.Background(), definition, input)
	if err != nil || !result.Valid || result.Result == nil || result.Result.Score == nil || *result.Result.Score != 1 {
		t.Fatalf("ValidateEvaluator() = %#v, %v", result, err)
	}
	if requestCount != 1 {
		t.Fatalf("authorized request count = %d, want 1", requestCount)
	}

	// An active grant is rechecked before each content-bearing request.
	if err := RevokeContentConsent(client.consentPath, "", time.Now()); err != nil {
		t.Fatalf("RevokeContentConsent() error = %v", err)
	}
	if _, err := client.ValidateEvaluator(context.Background(), definition, input); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("ValidateEvaluator() after revocation error = %v", err)
	}
	if requestCount != 1 {
		t.Fatalf("request count after revocation = %d, want 1", requestCount)
	}
}

type evaluatorTestRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper evaluatorTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestEvaluatorContentRequestsPropagateCancellationAndTimeout(t *testing.T) {
	definition := loadCozeLoopTestEvaluator(t, "answer-nonempty")
	for _, testCase := range []struct {
		name          string
		wantErrorText string
		withTimeout   bool
	}{
		{name: "cancelled", wantErrorText: "cancelled"},
		{name: "deadline", wantErrorText: "timeout", withTimeout: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			transport := evaluatorTestRoundTripper(func(request *http.Request) (*http.Response, error) {
				entered <- struct{}{}
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
			client, err := newOpenAPIEvaluationClient("http://127.0.0.1:32145", "12345", "test-token", &http.Client{Transport: transport})
			if err != nil {
				t.Fatalf("newOpenAPIEvaluationClient() error = %v", err)
			}
			client.contentUploadEnabled = true
			client.consentPath = filepath.Join(t.TempDir(), "consent.json")
			if err := GrantContentConsent(client.consentPath, "12345", "http://127.0.0.1:32145", []string{ConsentScopeEvaluationContent}, time.Now()); err != nil {
				t.Fatalf("GrantContentConsent() error = %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			if testCase.withTimeout {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
			}
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, requestErr := client.ValidateEvaluator(ctx, definition, codeEvaluatorTestInput("candidate"))
				result <- requestErr
			}()
			select {
			case <-entered:
			case requestErr := <-result:
				t.Fatalf("request ended before transport: %v", requestErr)
			case <-time.After(time.Second):
				t.Fatal("request did not reach transport")
			}
			if !testCase.withTimeout {
				cancel()
			}
			select {
			case requestErr := <-result:
				if requestErr == nil || !strings.Contains(requestErr.Error(), testCase.wantErrorText) {
					t.Fatalf("request error = %v, want %q", requestErr, testCase.wantErrorText)
				}
			case <-time.After(time.Second):
				t.Fatal("request did not stop after cancellation/deadline")
			}
		})
	}
}

func TestEvaluatorErrorsDoNotLeakResponseBodiesOrCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeEvaluatorTestEnvelope(t, writer, http.StatusInternalServerError, nil)
	}))
	defer server.Close()
	client := newEvaluatorTestClient(t, server.URL)
	_, err := client.ListEvaluators(context.Background(), eval.EvaluatorListFilter{Name: "name"})
	if err == nil || strings.Contains(err.Error(), "secret response detail") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("unsafe evaluator error = %v", err)
	}
}
