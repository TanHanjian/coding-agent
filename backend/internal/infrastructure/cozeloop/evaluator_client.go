package cozeloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"interview-memory-agent/backend/internal/eval"
)

const (
	cozeEvaluatorTypePrompt int64 = 1
	cozeEvaluatorTypeCode   int64 = 2
	cozePromptSourceCustom  int64 = 3
	cozeMessageRoleSystem   int64 = 1
	evaluatorPageSize             = 100
)

func (c *OpenAPIEvaluationClient) ListEvaluators(ctx context.Context, filter eval.EvaluatorListFilter) ([]eval.EvaluatorReference, error) {
	if filter.Type != "" && filter.Type != eval.EvaluatorTypeCode && filter.Type != eval.EvaluatorTypePrompt {
		return nil, errors.New("unsupported evaluator type filter")
	}
	results := make([]eval.EvaluatorReference, 0)
	for page := 1; page <= maxEvaluationPageCount; page++ {
		body := map[string]any{
			"workspace_id": c.workspaceID,
			"with_version": true,
			"page_number":  page,
			"page_size":    evaluatorPageSize,
		}
		if filter.Name != "" {
			body["search_name"] = filter.Name
		}
		if filter.Type != "" {
			value, _ := evaluatorTypeValue(filter.Type)
			body["evaluator_type"] = []int64{value}
		}
		var response struct {
			Evaluators []struct {
				ID   json.RawMessage `json:"evaluator_id"`
				Name string          `json:"name"`
				Type json.RawMessage `json:"evaluator_type"`
			} `json:"evaluators"`
			Total json.RawMessage `json:"total"`
		}
		if err := c.requestMetadata(ctx, http.MethodPost, "/api/evaluation/v1/evaluators/list", nil, body, &response); err != nil {
			return nil, err
		}
		for _, remote := range response.Evaluators {
			if filter.Name != "" && remote.Name != filter.Name {
				continue
			}
			typeValue, err := parseEvaluatorType(remote.Type)
			if err != nil {
				return nil, err
			}
			if filter.Type != "" && typeValue != filter.Type {
				continue
			}
			id, err := parsePositiveEvaluatorID(remote.ID)
			if err != nil {
				return nil, err
			}
			results = append(results, eval.EvaluatorReference{ID: id, Name: remote.Name, Type: typeValue})
		}
		if len(response.Evaluators) < evaluatorPageSize || evaluatorTotalReached(response.Total, len(results)) {
			return results, nil
		}
	}
	return nil, errors.New("CozeLoop evaluator list exceeded the page limit")
}

func (c *OpenAPIEvaluationClient) CreateEvaluator(ctx context.Context, definition eval.EvaluatorDefinition) (eval.EvaluatorReference, error) {
	if err := definition.Validate(); err != nil {
		return eval.EvaluatorReference{}, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	evaluatorType, err := evaluatorTypeValue(definition.Type)
	if err != nil {
		return eval.EvaluatorReference{}, err
	}
	body := map[string]any{
		"workspace_id": c.workspaceID,
		"evaluator": map[string]any{
			"evaluator_type": evaluatorType,
			"name":           definition.Name,
			"description":    definition.Description,
		},
	}
	var response struct {
		EvaluatorID json.RawMessage `json:"evaluator_id"`
	}
	if err := c.requestMetadata(ctx, http.MethodPost, "/api/evaluation/v1/evaluators", nil, body, &response); err != nil {
		return eval.EvaluatorReference{}, err
	}
	id, err := parsePositiveEvaluatorID(response.EvaluatorID)
	if err != nil {
		return eval.EvaluatorReference{}, errors.New("CozeLoop evaluator API returned an invalid evaluator ID")
	}
	return eval.EvaluatorReference{ID: id, Name: definition.Name, Type: definition.Type}, nil
}

func (c *OpenAPIEvaluationClient) UpdateEvaluatorDraft(ctx context.Context, evaluatorID string, definition eval.EvaluatorDefinition) (eval.EvaluatorReference, error) {
	id, endpoint, err := evaluatorEndpoint(evaluatorID, "update_draft")
	if err != nil {
		return eval.EvaluatorReference{}, err
	}
	if err := definition.Validate(); err != nil {
		return eval.EvaluatorReference{}, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	evaluatorType, err := evaluatorTypeValue(definition.Type)
	if err != nil {
		return eval.EvaluatorReference{}, err
	}
	content, err := evaluatorContentPayload(definition)
	if err != nil {
		return eval.EvaluatorReference{}, err
	}
	body := map[string]any{
		"workspace_id":      c.workspaceID,
		"evaluator_type":    evaluatorType,
		"evaluator_content": content,
	}
	var response struct {
		Evaluator struct {
			ID   json.RawMessage `json:"evaluator_id"`
			Name string          `json:"name"`
		} `json:"evaluator"`
	}
	if err := c.requestMetadata(ctx, http.MethodPatch, endpoint, nil, body, &response); err != nil {
		return eval.EvaluatorReference{}, err
	}
	if len(response.Evaluator.ID) > 0 && string(response.Evaluator.ID) != "null" {
		responseID, err := parsePositiveEvaluatorID(response.Evaluator.ID)
		if err != nil || responseID != id {
			return eval.EvaluatorReference{}, errors.New("CozeLoop evaluator API returned an unexpected evaluator reference")
		}
	}
	if response.Evaluator.Name != "" && response.Evaluator.Name != definition.Name {
		return eval.EvaluatorReference{}, errors.New("CozeLoop evaluator API returned an unexpected evaluator name")
	}
	return eval.EvaluatorReference{ID: id, Name: definition.Name, Type: definition.Type}, nil
}

func (c *OpenAPIEvaluationClient) ListEvaluatorVersions(ctx context.Context, evaluatorID string) ([]eval.EvaluatorVersionReference, error) {
	_, endpoint, err := evaluatorEndpoint(evaluatorID, "versions/list")
	if err != nil {
		return nil, err
	}
	versions := make([]eval.EvaluatorVersionReference, 0)
	for page := 1; page <= maxEvaluationPageCount; page++ {
		body := map[string]any{
			"workspace_id": c.workspaceID,
			"page_number":  page,
			"page_size":    evaluatorPageSize,
		}
		var response struct {
			Versions []struct {
				ID               json.RawMessage `json:"id"`
				Version          string          `json:"version"`
				EvaluatorContent json.RawMessage `json:"evaluator_content"`
			} `json:"evaluator_versions"`
			Total json.RawMessage `json:"total"`
		}
		if err := c.requestMetadata(ctx, http.MethodPost, endpoint, nil, body, &response); err != nil {
			return nil, err
		}
		for _, remote := range response.Versions {
			versionID := ""
			if len(remote.ID) > 0 && string(remote.ID) != "null" {
				versionID, err = parsePositiveEvaluatorID(remote.ID)
				if err != nil {
					return nil, errors.New("CozeLoop evaluator API returned an invalid evaluator version ID")
				}
			}
			if strings.TrimSpace(remote.Version) == "" {
				return nil, errors.New("CozeLoop evaluator API returned an invalid evaluator version")
			}
			contentHash, err := evaluatorContentHashFromPayload(remote.EvaluatorContent)
			if err != nil {
				return nil, errors.New("cannot verify CozeLoop evaluator version content")
			}
			versions = append(versions, eval.EvaluatorVersionReference{ID: versionID, Version: remote.Version, ContentHash: contentHash})
		}
		if len(response.Versions) < evaluatorPageSize || evaluatorTotalReached(response.Total, len(versions)) {
			return versions, nil
		}
	}
	return nil, errors.New("CozeLoop evaluator version list exceeded the page limit")
}

func (c *OpenAPIEvaluationClient) SubmitEvaluatorVersion(ctx context.Context, evaluatorID, version, description string) (eval.EvaluatorVersionReference, error) {
	_, endpoint, err := evaluatorEndpoint(evaluatorID, "submit_version")
	if err != nil {
		return eval.EvaluatorVersionReference{}, err
	}
	if strings.TrimSpace(version) == "" || strings.TrimSpace(version) != version {
		return eval.EvaluatorVersionReference{}, errors.New("evaluator version is required")
	}
	body := map[string]any{
		"workspace_id": c.workspaceID,
		"version":      version,
		"description":  description,
	}
	var response struct {
		Evaluator struct {
			CurrentVersion struct {
				ID      json.RawMessage `json:"id"`
				Version string          `json:"version"`
			} `json:"current_version"`
		} `json:"evaluator"`
	}
	if err := c.requestMetadata(ctx, http.MethodPost, endpoint, nil, body, &response); err != nil {
		return eval.EvaluatorVersionReference{}, err
	}
	if response.Evaluator.CurrentVersion.Version != "" && response.Evaluator.CurrentVersion.Version != version {
		return eval.EvaluatorVersionReference{}, errors.New("CozeLoop evaluator API submitted an unexpected version")
	}
	versionID := ""
	if len(response.Evaluator.CurrentVersion.ID) > 0 && string(response.Evaluator.CurrentVersion.ID) != "null" {
		versionID, err = parsePositiveEvaluatorID(response.Evaluator.CurrentVersion.ID)
		if err != nil {
			return eval.EvaluatorVersionReference{}, errors.New("CozeLoop evaluator API returned an invalid evaluator version ID")
		}
	}
	return eval.EvaluatorVersionReference{ID: versionID, Version: version}, nil
}

func (c *OpenAPIEvaluationClient) ValidateEvaluator(ctx context.Context, definition eval.EvaluatorDefinition, input eval.EvaluatorInputData) (eval.EvaluatorValidationResult, error) {
	evaluatorType, content, err := evaluatorRequestParts(definition)
	if err != nil {
		return eval.EvaluatorValidationResult{}, err
	}
	input, err = normalizeEvaluatorInput(definition, input)
	if err != nil {
		return eval.EvaluatorValidationResult{}, err
	}
	body := map[string]any{
		"workspace_id":      c.workspaceID,
		"evaluator_type":    evaluatorType,
		"evaluator_content": content,
		"input_data":        input,
	}
	var response struct {
		Valid               bool            `json:"valid"`
		EvaluatorOutputData json.RawMessage `json:"evaluator_output_data"`
	}
	if err := c.requestContent(ctx, http.MethodPost, "/api/evaluation/v1/evaluators/validate", nil, body, &response); err != nil {
		return eval.EvaluatorValidationResult{}, err
	}
	result, err := parseEvaluatorExecutionResult(response.EvaluatorOutputData)
	if err != nil {
		return eval.EvaluatorValidationResult{}, err
	}
	if response.Valid && result == nil {
		return eval.EvaluatorValidationResult{}, errors.New("CozeLoop evaluator validation returned no output for the supplied input")
	}
	if err := validateEvaluatorExecutionResult(definition, result); err != nil {
		return eval.EvaluatorValidationResult{}, err
	}
	return eval.EvaluatorValidationResult{Valid: response.Valid, Result: result}, nil
}

func (c *OpenAPIEvaluationClient) BatchDebugEvaluator(ctx context.Context, definition eval.EvaluatorDefinition, inputs []eval.EvaluatorInputData) ([]eval.EvaluatorDebugResult, error) {
	if len(inputs) == 0 {
		return nil, errors.New("evaluator debug inputs are required")
	}
	if len(inputs) > maxEvaluationBatchSize {
		return nil, fmt.Errorf("evaluator debug batch exceeds the limit of %d", maxEvaluationBatchSize)
	}
	evaluatorType, content, err := evaluatorRequestParts(definition)
	if err != nil {
		return nil, err
	}
	normalizedInputs := make([]eval.EvaluatorInputData, len(inputs))
	for index, input := range inputs {
		normalizedInputs[index], err = normalizeEvaluatorInput(definition, input)
		if err != nil {
			return nil, fmt.Errorf("invalid evaluator debug input at index %d: %w", index, err)
		}
	}
	body := map[string]any{
		"workspace_id":      c.workspaceID,
		"evaluator_type":    evaluatorType,
		"evaluator_content": content,
		"input_data":        normalizedInputs,
	}
	var response struct {
		Outputs []json.RawMessage `json:"evaluator_output_data"`
	}
	if err := c.requestContent(ctx, http.MethodPost, "/api/evaluation/v1/evaluators/batch_debug", nil, body, &response); err != nil {
		return nil, err
	}
	if len(response.Outputs) != len(inputs) {
		return nil, errors.New("CozeLoop evaluator API returned an incomplete debug batch")
	}
	results := make([]eval.EvaluatorDebugResult, len(response.Outputs))
	for index, output := range response.Outputs {
		result, err := parseEvaluatorExecutionResult(output)
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, errors.New("CozeLoop evaluator API returned a missing debug result")
		}
		if err := validateEvaluatorExecutionResult(definition, result); err != nil {
			return nil, err
		}
		results[index] = eval.EvaluatorDebugResult{InputIndex: index, Result: *result}
	}
	return results, nil
}

func evaluatorRequestParts(definition eval.EvaluatorDefinition) (int64, map[string]any, error) {
	if err := definition.Validate(); err != nil {
		return 0, nil, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	content, err := evaluatorContentPayload(definition)
	if err != nil {
		return 0, nil, err
	}
	evaluatorType, err := evaluatorTypeValue(definition.Type)
	if err != nil {
		return 0, nil, err
	}
	return evaluatorType, content, nil
}

func normalizeEvaluatorInput(definition eval.EvaluatorDefinition, input eval.EvaluatorInputData) (eval.EvaluatorInputData, error) {
	allowed := make(map[string]eval.EvaluatorFieldSchema, len(definition.InputSchemas))
	for _, schema := range definition.InputSchemas {
		allowed[schema.Key] = schema
	}
	normalized := eval.EvaluatorInputData{}
	switch definition.Type {
	case eval.EvaluatorTypePrompt:
		if len(input.EvaluateDatasetFields) > 0 || len(input.EvaluateTargetOutputFields) > 0 {
			return eval.EvaluatorInputData{}, errors.New("Prompt evaluator inputs must use input_fields")
		}
		if err := rejectUndeclaredEvaluatorFields(input.InputFields, allowed); err != nil {
			return eval.EvaluatorInputData{}, err
		}
		normalized.InputFields = make(map[string]eval.EvaluatorFieldContent, len(allowed))
		for _, schema := range definition.InputSchemas {
			content, err := evaluatorInputContent(input.InputFields, schema)
			if err != nil {
				return eval.EvaluatorInputData{}, err
			}
			normalized.InputFields[schema.Key] = content
		}
	case eval.EvaluatorTypeCode:
		if len(input.InputFields) > 0 {
			return eval.EvaluatorInputData{}, errors.New("Code evaluator inputs must use dataset and target-output fields")
		}
		allowedDataset := make(map[string]eval.EvaluatorFieldSchema, len(allowed))
		allowedOutput := make(map[string]eval.EvaluatorFieldSchema, 1)
		for _, schema := range definition.InputSchemas {
			if schema.Key == "actual_output" {
				allowedOutput[schema.Key] = schema
			} else {
				allowedDataset[schema.Key] = schema
			}
		}
		if err := rejectUndeclaredEvaluatorFields(input.EvaluateDatasetFields, allowedDataset); err != nil {
			return eval.EvaluatorInputData{}, err
		}
		if err := rejectUndeclaredEvaluatorFields(input.EvaluateTargetOutputFields, allowedOutput); err != nil {
			return eval.EvaluatorInputData{}, err
		}
		normalized.EvaluateDatasetFields = make(map[string]eval.EvaluatorFieldContent, len(allowedDataset))
		normalized.EvaluateTargetOutputFields = make(map[string]eval.EvaluatorFieldContent, len(allowedOutput))
		for _, schema := range definition.InputSchemas {
			fieldGroup := input.EvaluateDatasetFields
			outputGroup := normalized.EvaluateDatasetFields
			if schema.Key == "actual_output" {
				fieldGroup = input.EvaluateTargetOutputFields
				outputGroup = normalized.EvaluateTargetOutputFields
			}
			content, err := evaluatorInputContent(fieldGroup, schema)
			if err != nil {
				return eval.EvaluatorInputData{}, err
			}
			outputGroup[schema.Key] = content
		}
	default:
		return eval.EvaluatorInputData{}, errors.New("unsupported evaluator type")
	}
	// Ext is intentionally not copied: it is not part of the evaluator's
	// declared case inputs and is not needed for ordered BatchDebug correlation.
	return normalized, nil
}

func rejectUndeclaredEvaluatorFields(fields map[string]eval.EvaluatorFieldContent, allowed map[string]eval.EvaluatorFieldSchema) error {
	for key := range fields {
		if _, exists := allowed[key]; !exists {
			return errors.New("evaluator input contains an undeclared or misplaced field")
		}
	}
	return nil
}

func evaluatorInputContent(fields map[string]eval.EvaluatorFieldContent, schema eval.EvaluatorFieldSchema) (eval.EvaluatorFieldContent, error) {
	content, exists := fields[schema.Key]
	if !exists {
		if schema.Required {
			return eval.EvaluatorFieldContent{}, fmt.Errorf("required evaluator input field %q is missing", schema.Key)
		}
		return eval.EvaluatorFieldContent{ContentType: eval.EvaluatorContentTypeText, Text: ""}, nil
	}
	if content.ContentType != eval.EvaluatorContentTypeText {
		return eval.EvaluatorFieldContent{}, fmt.Errorf("evaluator input field %q must use Text content", schema.Key)
	}
	return content, nil
}

func evaluatorContentPayload(definition eval.EvaluatorDefinition) (map[string]any, error) {
	inputSchemas, err := evaluatorSchemaPayload(definition.InputSchemas)
	if err != nil {
		return nil, err
	}
	outputSchemas, err := evaluatorSchemaPayload(definition.OutputSchemas)
	if err != nil {
		return nil, err
	}
	content := map[string]any{
		"receive_chat_history": false,
		"input_schemas":        inputSchemas,
		"output_schemas":       outputSchemas,
	}
	switch definition.Type {
	case eval.EvaluatorTypeCode:
		content["code_evaluator"] = map[string]any{
			"language_type": "Python",
			"code_content":  definition.AssetContent,
		}
	case eval.EvaluatorTypePrompt:
		content["prompt_evaluator"] = map[string]any{
			"message_list": []any{map[string]any{
				"role": cozeMessageRoleSystem,
				"content": map[string]any{
					"content_type": eval.EvaluatorContentTypeText,
					"text":         definition.AssetContent,
				},
			}},
			"prompt_source_type": cozePromptSourceCustom,
		}
	default:
		return nil, errors.New("unsupported evaluator type")
	}
	return content, nil
}

func evaluatorSchemaPayload(schemas []eval.EvaluatorFieldSchema) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(schemas))
	for _, schema := range schemas {
		jsonSchema := map[string]any{"type": schema.Type}
		if schema.Minimum != nil {
			jsonSchema["minimum"] = *schema.Minimum
		}
		if schema.Maximum != nil {
			jsonSchema["maximum"] = *schema.Maximum
		}
		encoded, err := json.Marshal(jsonSchema)
		if err != nil {
			return nil, errors.New("cannot encode evaluator field schema")
		}
		field := map[string]any{
			"key":                   schema.Key,
			"support_content_types": []string{eval.EvaluatorContentTypeText},
			"json_schema":           string(encoded),
		}
		if !schema.Required {
			field["default_value"] = map[string]any{
				"content_type": eval.EvaluatorContentTypeText,
				"text":         "",
			}
		}
		result = append(result, field)
	}
	return result, nil
}

func evaluatorTypeValue(evaluatorType eval.EvaluatorType) (int64, error) {
	switch evaluatorType {
	case eval.EvaluatorTypePrompt:
		return cozeEvaluatorTypePrompt, nil
	case eval.EvaluatorTypeCode:
		return cozeEvaluatorTypeCode, nil
	default:
		return 0, errors.New("unsupported evaluator type")
	}
}

func parseEvaluatorType(value json.RawMessage) (eval.EvaluatorType, error) {
	if len(value) == 0 || string(value) == "null" {
		return "", errors.New("CozeLoop evaluator API returned a missing evaluator type")
	}
	var name string
	if err := json.Unmarshal(value, &name); err == nil {
		switch name {
		case "Prompt":
			return eval.EvaluatorTypePrompt, nil
		case "Code":
			return eval.EvaluatorTypeCode, nil
		default:
			return "", errors.New("CozeLoop evaluator API returned an unsupported evaluator type")
		}
	}
	var numeric int64
	if err := json.Unmarshal(value, &numeric); err != nil {
		return "", errors.New("CozeLoop evaluator API returned an invalid evaluator type")
	}
	switch numeric {
	case cozeEvaluatorTypePrompt:
		return eval.EvaluatorTypePrompt, nil
	case cozeEvaluatorTypeCode:
		return eval.EvaluatorTypeCode, nil
	default:
		return "", errors.New("CozeLoop evaluator API returned an unsupported evaluator type")
	}
}

func parsePositiveEvaluatorID(value json.RawMessage) (string, error) {
	id, err := parsePlatformID(value)
	if err != nil {
		return "", err
	}
	parsed, err := strconv.ParseInt(id, 10, 64)
	if err != nil || parsed <= 0 {
		return "", errors.New("evaluator ID must be a positive integer")
	}
	return strconv.FormatInt(parsed, 10), nil
}

func evaluatorEndpoint(evaluatorID, suffix string) (string, string, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(evaluatorID), 10, 64)
	if err != nil || parsed <= 0 {
		return "", "", errors.New("evaluator ID must be a positive integer")
	}
	id := strconv.FormatInt(parsed, 10)
	return id, path.Join("/api/evaluation/v1/evaluators", url.PathEscape(id), suffix), nil
}

func evaluatorTotalReached(raw json.RawMessage, count int) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	value, err := parsePlatformID(raw)
	if err != nil {
		return false
	}
	total, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	return total >= 0 && int64(count) >= total
}

type remoteEvaluatorSchema struct {
	Key                 string   `json:"key"`
	SupportContentTypes []string `json:"support_content_types"`
	JSONSchema          string   `json:"json_schema"`
	DefaultValue        *struct {
		ContentType string `json:"content_type"`
		Text        string `json:"text"`
	} `json:"default_value"`
}

type remoteJSONSchema struct {
	Type    string          `json:"type"`
	Minimum json.RawMessage `json:"minimum"`
	Maximum json.RawMessage `json:"maximum"`
}

func evaluatorContentHashFromPayload(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("evaluator content is missing")
	}
	var remote struct {
		ReceiveChatHistory bool                    `json:"receive_chat_history"`
		InputSchemas       []remoteEvaluatorSchema `json:"input_schemas"`
		OutputSchemas      []remoteEvaluatorSchema `json:"output_schemas"`
		CodeEvaluator      *struct {
			LanguageType string `json:"language_type"`
			CodeContent  string `json:"code_content"`
		} `json:"code_evaluator"`
		PromptEvaluator *struct {
			MessageList []struct {
				Role    json.RawMessage `json:"role"`
				Content struct {
					ContentType string `json:"content_type"`
					Text        string `json:"text"`
				} `json:"content"`
			} `json:"message_list"`
			PromptSourceType json.RawMessage `json:"prompt_source_type"`
		} `json:"prompt_evaluator"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&remote); err != nil {
		return "", errors.New("evaluator content is invalid")
	}
	if remote.ReceiveChatHistory {
		return "", errors.New("evaluator content has unsupported chat history settings")
	}
	definition := eval.EvaluatorDefinition{
		Key:           "remote-evaluator",
		Name:          "Remote evaluator",
		Version:       "1.0.0",
		AssetFile:     "remote.asset",
		InputSchemas:  make([]eval.EvaluatorFieldSchema, 0, len(remote.InputSchemas)),
		OutputSchemas: make([]eval.EvaluatorFieldSchema, 0, len(remote.OutputSchemas)),
	}
	var err error
	definition.InputSchemas, err = evaluatorSchemasFromRemote(remote.InputSchemas)
	if err != nil {
		return "", err
	}
	definition.OutputSchemas, err = evaluatorSchemasFromRemote(remote.OutputSchemas)
	if err != nil {
		return "", err
	}
	if remote.CodeEvaluator != nil && remote.PromptEvaluator == nil {
		if remote.CodeEvaluator.LanguageType != "Python" || strings.TrimSpace(remote.CodeEvaluator.CodeContent) == "" {
			return "", errors.New("code evaluator content is incomplete")
		}
		definition.Type = eval.EvaluatorTypeCode
		definition.AssetContent = remote.CodeEvaluator.CodeContent
	} else if remote.PromptEvaluator != nil && remote.CodeEvaluator == nil {
		if len(remote.PromptEvaluator.MessageList) != 1 || remote.PromptEvaluator.MessageList[0].Content.ContentType != eval.EvaluatorContentTypeText {
			return "", errors.New("prompt evaluator content is unsupported")
		}
		role, err := parseEnumValue(remote.PromptEvaluator.MessageList[0].Role, cozeMessageRoleSystem, "System")
		if err != nil || role != cozeMessageRoleSystem {
			return "", errors.New("prompt evaluator message role is unsupported")
		}
		promptSourceType, err := parseEnumValue(remote.PromptEvaluator.PromptSourceType, cozePromptSourceCustom, "Custom")
		if err != nil || promptSourceType != cozePromptSourceCustom {
			return "", errors.New("prompt evaluator source type is unsupported")
		}
		definition.Type = eval.EvaluatorTypePrompt
		definition.AssetContent = remote.PromptEvaluator.MessageList[0].Content.Text
	} else {
		return "", errors.New("evaluator content has an unsupported type")
	}
	return eval.EvaluatorPlatformContentHash(definition)
}

func evaluatorSchemasFromRemote(remote []remoteEvaluatorSchema) ([]eval.EvaluatorFieldSchema, error) {
	result := make([]eval.EvaluatorFieldSchema, 0, len(remote))
	for _, schema := range remote {
		if schema.Key == "" || len(schema.SupportContentTypes) != 1 || schema.SupportContentTypes[0] != eval.EvaluatorContentTypeText || schema.JSONSchema == "" {
			return nil, errors.New("evaluator schema is unsupported")
		}
		var jsonSchema remoteJSONSchema
		decoder := json.NewDecoder(strings.NewReader(schema.JSONSchema))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&jsonSchema); err != nil || jsonSchema.Type == "" {
			return nil, errors.New("evaluator schema is invalid")
		}
		field := eval.EvaluatorFieldSchema{Key: schema.Key, Type: jsonSchema.Type, Required: true}
		if schema.DefaultValue != nil {
			if schema.DefaultValue.ContentType != eval.EvaluatorContentTypeText || schema.DefaultValue.Text != "" {
				return nil, errors.New("evaluator schema default value is unsupported")
			}
			field.Required = false
		}
		if len(jsonSchema.Minimum) > 0 && string(jsonSchema.Minimum) != "null" {
			var minimum float64
			if err := json.Unmarshal(jsonSchema.Minimum, &minimum); err != nil {
				return nil, errors.New("evaluator schema minimum is invalid")
			}
			field.Minimum = &minimum
		}
		if len(jsonSchema.Maximum) > 0 && string(jsonSchema.Maximum) != "null" {
			var maximum float64
			if err := json.Unmarshal(jsonSchema.Maximum, &maximum); err != nil {
				return nil, errors.New("evaluator schema maximum is invalid")
			}
			field.Maximum = &maximum
		}
		result = append(result, field)
	}
	return result, nil
}

func parseEnumValue(raw json.RawMessage, expected int64, expectedName string) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, errors.New("enum value is missing")
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return numeric, nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil && name == expectedName {
		return expected, nil
	}
	return 0, errors.New("enum value is invalid")
}

func parseEvaluatorExecutionResult(raw json.RawMessage) (*eval.EvaluatorExecutionResult, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var response struct {
		EvaluatorResult *struct {
			Score      *float64 `json:"score"`
			Reasoning  *string  `json:"reasoning"`
			Correction *struct {
				Explain *string `json:"explain"`
			} `json:"correction"`
		} `json:"evaluator_result"`
		EvaluatorRunError *struct{} `json:"evaluator_run_error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, errors.New("CozeLoop evaluator API returned an invalid evaluator result")
	}
	if response.EvaluatorRunError != nil {
		return &eval.EvaluatorExecutionResult{Status: "fail", ErrorCategory: "evaluator_error"}, nil
	}
	if response.EvaluatorResult == nil || response.EvaluatorResult.Score == nil {
		return nil, errors.New("CozeLoop evaluator API returned an incomplete evaluator result")
	}
	result := &eval.EvaluatorExecutionResult{Status: "success", Score: response.EvaluatorResult.Score}
	if response.EvaluatorResult.Reasoning != nil {
		result.Reason = *response.EvaluatorResult.Reasoning
	}
	if strings.TrimSpace(result.Reason) == "" && response.EvaluatorResult.Correction != nil && response.EvaluatorResult.Correction.Explain != nil {
		result.Reason = *response.EvaluatorResult.Correction.Explain
	}
	if strings.TrimSpace(result.Reason) == "" {
		return nil, errors.New("CozeLoop evaluator API returned an evaluator result without a reason")
	}
	return result, nil
}

func validateEvaluatorExecutionResult(definition eval.EvaluatorDefinition, result *eval.EvaluatorExecutionResult) error {
	if result == nil || result.Status != "success" || result.Score == nil {
		return nil
	}
	for _, schema := range definition.OutputSchemas {
		if schema.Key != "score" {
			continue
		}
		if schema.Minimum != nil && *result.Score < *schema.Minimum {
			return errors.New("CozeLoop evaluator score is below the declared range")
		}
		if schema.Maximum != nil && *result.Score > *schema.Maximum {
			return errors.New("CozeLoop evaluator score is above the declared range")
		}
		break
	}
	return nil
}

var _ eval.EvaluatorPlatform = (*OpenAPIEvaluationClient)(nil)
