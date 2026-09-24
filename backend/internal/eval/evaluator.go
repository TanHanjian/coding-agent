package eval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Evaluator domain types, input mapping, and publication workflow.
const EvaluatorContentTypeText = "Text"

type EvaluatorFieldContent struct {
	ContentType string `json:"content_type"`
	Text        string `json:"text"`
}

type EvaluatorInputData struct {
	InputFields                map[string]EvaluatorFieldContent `json:"input_fields,omitempty"`
	EvaluateDatasetFields      map[string]EvaluatorFieldContent `json:"evaluate_dataset_fields,omitempty"`
	EvaluateTargetOutputFields map[string]EvaluatorFieldContent `json:"evaluate_target_output_fields,omitempty"`
	Ext                        map[string]string                `json:"ext,omitempty"`
}

type EvaluatorListFilter struct {
	Name string
	Type EvaluatorType
}

type EvaluatorReference struct {
	ID   string
	Name string
	Type EvaluatorType
}

type EvaluatorVersionReference struct {
	ID          string
	Version     string
	ContentHash string
}

type EvaluatorExecutionResult struct {
	Status        string
	Score         *float64
	Reason        string
	ErrorCategory string
}

type EvaluatorValidationResult struct {
	Valid  bool
	Result *EvaluatorExecutionResult
}

type EvaluatorDebugResult struct {
	InputIndex int
	Result     EvaluatorExecutionResult
}

// EvaluatorPlatform is the application boundary for the evaluator operations
// used by the CozeLoop workflow. Its data types intentionally contain no HTTP
// or generated API DTOs.
type EvaluatorPlatform interface {
	ListEvaluators(context.Context, EvaluatorListFilter) ([]EvaluatorReference, error)
	CreateEvaluator(context.Context, EvaluatorDefinition) (EvaluatorReference, error)
	UpdateEvaluatorDraft(context.Context, string, EvaluatorDefinition) (EvaluatorReference, error)
	ListEvaluatorVersions(context.Context, string) ([]EvaluatorVersionReference, error)
	SubmitEvaluatorVersion(context.Context, string, string, string) (EvaluatorVersionReference, error)
	ValidateEvaluator(context.Context, EvaluatorDefinition, EvaluatorInputData) (EvaluatorValidationResult, error)
	BatchDebugEvaluator(context.Context, EvaluatorDefinition, []EvaluatorInputData) ([]EvaluatorDebugResult, error)
}

// BuildEvaluatorInputBatch maps a local report into the documented CozeLoop
// input shape. Only fields declared by the evaluator are included; invalid
// infrastructure samples are omitted, and the order of valid report rows is
// retained for BatchDebug result correlation.
func BuildEvaluatorInputBatch(definition EvaluatorDefinition, cases []EvalCase, report RunReport) ([]EvaluatorInputData, error) {
	if err := definition.Validate(); err != nil {
		return nil, fmt.Errorf("invalid evaluator definition: %w", err)
	}

	caseIDs := make(map[string]struct{}, len(cases))
	for _, currentCase := range cases {
		if _, duplicate := caseIDs[currentCase.ID]; duplicate {
			return nil, errors.New("evaluation cases contain duplicate IDs")
		}
		caseIDs[currentCase.ID] = struct{}{}
	}

	items, err := BuildDatasetItems(cases, report)
	if err != nil {
		return nil, fmt.Errorf("build evaluator source fields: %w", err)
	}
	if len(items) != len(report.Cases) {
		return nil, errors.New("evaluation result mapping is incomplete")
	}

	inputs := make([]EvaluatorInputData, 0, len(items))
	for index, result := range report.Cases {
		if result.InfrastructureFailure {
			continue
		}
		item := items[index]
		input := EvaluatorInputData{}
		if definition.Type == EvaluatorTypePrompt {
			input.InputFields = make(map[string]EvaluatorFieldContent, len(definition.InputSchemas))
		} else {
			input.EvaluateDatasetFields = make(map[string]EvaluatorFieldContent)
			input.EvaluateTargetOutputFields = make(map[string]EvaluatorFieldContent)
		}

		for _, schema := range definition.InputSchemas {
			value, exists := item.Fields[schema.Key]
			if !exists {
				if schema.Required {
					return nil, fmt.Errorf("evaluator input field %q is unavailable", schema.Key)
				}
				value = ""
			}
			content := EvaluatorFieldContent{ContentType: EvaluatorContentTypeText, Text: value}
			if definition.Type == EvaluatorTypePrompt {
				input.InputFields[schema.Key] = content
			} else if schema.Key == "actual_output" {
				input.EvaluateTargetOutputFields[schema.Key] = content
			} else {
				input.EvaluateDatasetFields[schema.Key] = content
			}
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

// EnsureEvaluatorResource reuses only one exact-name evaluator with the same
// type. If creation races with another client, it re-reads the exact name once
// and reuses only a compatible resource.
func EnsureEvaluatorResource(ctx context.Context, platform EvaluatorPlatform, definition EvaluatorDefinition) (EvaluatorReference, error) {
	if platform == nil {
		return EvaluatorReference{}, errors.New("evaluator platform is required")
	}
	if err := definition.Validate(); err != nil {
		return EvaluatorReference{}, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	existing, err := listExactEvaluators(ctx, platform, definition.Name)
	if err != nil {
		return EvaluatorReference{}, err
	}
	if len(existing) > 1 {
		return EvaluatorReference{}, errors.New("evaluator name is not unique in the workspace")
	}
	if len(existing) == 1 {
		return validateEvaluatorReference(existing[0], definition)
	}

	created, createErr := platform.CreateEvaluator(ctx, definition)
	if createErr == nil {
		return validateEvaluatorReference(created, definition)
	}

	// A create may have committed remotely even when the response was lost.
	// Read back once and reuse only an exact name/type match.
	readback, readErr := listExactEvaluators(ctx, platform, definition.Name)
	if readErr == nil {
		if len(readback) == 1 {
			return validateEvaluatorReference(readback[0], definition)
		}
		if len(readback) > 1 {
			return EvaluatorReference{}, errors.New("evaluator name is not unique after create retry")
		}
	}
	return EvaluatorReference{}, fmt.Errorf("create evaluator failed: %w", createErr)
}

// FindEvaluatorVersion checks whether the exact immutable version already
// exists and matches the local platform-content hash.
func FindEvaluatorVersion(ctx context.Context, platform EvaluatorPlatform, evaluatorID string, definition EvaluatorDefinition) (EvaluatorVersionReference, bool, error) {
	if platform == nil {
		return EvaluatorVersionReference{}, false, errors.New("evaluator platform is required")
	}
	if strings.TrimSpace(evaluatorID) == "" {
		return EvaluatorVersionReference{}, false, errors.New("evaluator ID is required")
	}
	if err := definition.Validate(); err != nil {
		return EvaluatorVersionReference{}, false, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	expectedHash, err := EvaluatorPlatformContentHash(definition)
	if err != nil {
		return EvaluatorVersionReference{}, false, err
	}
	versions, err := platform.ListEvaluatorVersions(ctx, evaluatorID)
	if err != nil {
		return EvaluatorVersionReference{}, false, fmt.Errorf("list evaluator versions: %w", err)
	}
	return matchingEvaluatorVersion(versions, definition.Version, expectedHash)
}

// EnsureEvaluatorVersion submits an immutable evaluator version only when the
// exact version is absent. Existing and newly submitted versions are always
// read back and compared by platform-content hash before they are accepted.
func EnsureEvaluatorVersion(ctx context.Context, platform EvaluatorPlatform, evaluatorID string, definition EvaluatorDefinition) (EvaluatorVersionReference, error) {
	if platform == nil {
		return EvaluatorVersionReference{}, errors.New("evaluator platform is required")
	}
	if strings.TrimSpace(evaluatorID) == "" {
		return EvaluatorVersionReference{}, errors.New("evaluator ID is required")
	}
	if err := definition.Validate(); err != nil {
		return EvaluatorVersionReference{}, fmt.Errorf("invalid evaluator definition: %w", err)
	}
	if existing, found, err := FindEvaluatorVersion(ctx, platform, evaluatorID, definition); err != nil {
		return EvaluatorVersionReference{}, err
	} else if found {
		return existing, nil
	}

	submitted, submitErr := platform.SubmitEvaluatorVersion(ctx, evaluatorID, definition.Version, definition.Description)
	if submitErr != nil {
		// The submit response may be lost after the server commits. A readback is
		// the only safe retry; never submit the same version a second time blindly.
		existing, found, readErr := FindEvaluatorVersion(ctx, platform, evaluatorID, definition)
		if readErr != nil {
			return EvaluatorVersionReference{}, fmt.Errorf("verify evaluator version after submit failure: %w", readErr)
		}
		if found {
			return existing, nil
		}
		return EvaluatorVersionReference{}, fmt.Errorf("submit evaluator version failed: %w", submitErr)
	}
	if submitted.Version != "" && submitted.Version != definition.Version {
		return EvaluatorVersionReference{}, errors.New("CozeLoop submitted an unexpected evaluator version")
	}

	verified, found, err := FindEvaluatorVersion(ctx, platform, evaluatorID, definition)
	if err != nil {
		return EvaluatorVersionReference{}, fmt.Errorf("verify submitted evaluator version: %w", err)
	}
	if !found {
		return EvaluatorVersionReference{}, errors.New("submitted evaluator version was not found during readback")
	}
	return verified, nil
}

func listExactEvaluators(ctx context.Context, platform EvaluatorPlatform, name string) ([]EvaluatorReference, error) {
	results, err := platform.ListEvaluators(ctx, EvaluatorListFilter{Name: name})
	if err != nil {
		return nil, fmt.Errorf("list evaluators: %w", err)
	}
	exact := make([]EvaluatorReference, 0, len(results))
	for _, evaluator := range results {
		if evaluator.Name == name {
			exact = append(exact, evaluator)
		}
	}
	return exact, nil
}

func validateEvaluatorReference(reference EvaluatorReference, definition EvaluatorDefinition) (EvaluatorReference, error) {
	if reference.ID == "" || reference.Name != definition.Name || reference.Type != definition.Type {
		return EvaluatorReference{}, errors.New("CozeLoop evaluator name or type conflicts with the local definition")
	}
	return reference, nil
}

func matchingEvaluatorVersion(versions []EvaluatorVersionReference, version, expectedHash string) (EvaluatorVersionReference, bool, error) {
	var match *EvaluatorVersionReference
	for index := range versions {
		if versions[index].Version != version {
			continue
		}
		if match != nil {
			return EvaluatorVersionReference{}, false, errors.New("evaluator version is not unique in the workspace")
		}
		match = &versions[index]
	}
	if match == nil {
		return EvaluatorVersionReference{}, false, nil
	}
	if match.ContentHash == "" || match.ContentHash != expectedHash {
		return EvaluatorVersionReference{}, false, errors.New("existing evaluator version content conflicts with the local definition")
	}
	return *match, true, nil
}

// Manifest loading, asset validation, and content hashing.
const (
	EvaluatorManifestVersion = 1
	maxEvaluatorManifestSize = 1 << 20
	maxEvaluatorAssetSize    = 1 << 20
)

type EvaluatorType string

const (
	EvaluatorTypeCode   EvaluatorType = "code"
	EvaluatorTypePrompt EvaluatorType = "prompt"
)

type EvaluatorFieldSchema struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
}

type EvaluatorDefinition struct {
	Key           string                 `json:"key"`
	Name          string                 `json:"name"`
	Type          EvaluatorType          `json:"type"`
	Version       string                 `json:"version"`
	Description   string                 `json:"description,omitempty"`
	AssetFile     string                 `json:"assetFile"`
	InputSchemas  []EvaluatorFieldSchema `json:"inputSchemas"`
	OutputSchemas []EvaluatorFieldSchema `json:"outputSchemas"`

	AssetContent string `json:"-"`
	ContentHash  string `json:"-"`
}

type EvaluatorManifest struct {
	Version    int                   `json:"version"`
	Evaluators []EvaluatorDefinition `json:"evaluators"`
}

var evaluatorVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

var allowedEvaluatorInputFields = map[string]struct{}{
	"actual_output":     {},
	"execution_status":  {},
	"forbidden_content": {},
	"input_query":       {},
	"interview_context": {},
	"judge_rubric":      {},
	"required_facts":    {},
	"tool_trace":        {},
}

func LoadEvaluatorManifestFile(manifestPath string) (EvaluatorManifest, error) {
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return EvaluatorManifest{}, fmt.Errorf("open evaluator manifest: %w", err)
	}
	defer manifestFile.Close()

	manifestData, err := io.ReadAll(io.LimitReader(manifestFile, maxEvaluatorManifestSize+1))
	if err != nil {
		return EvaluatorManifest{}, errors.New("cannot read evaluator manifest")
	}
	if len(manifestData) > maxEvaluatorManifestSize {
		return EvaluatorManifest{}, errors.New("evaluator manifest exceeds the size limit")
	}

	decoder := json.NewDecoder(bytes.NewReader(manifestData))
	decoder.DisallowUnknownFields()
	var manifest EvaluatorManifest
	if err := decoder.Decode(&manifest); err != nil {
		return EvaluatorManifest{}, fmt.Errorf("invalid evaluator manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return EvaluatorManifest{}, fmt.Errorf("invalid evaluator manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return EvaluatorManifest{}, err
	}

	manifestDirectory, err := filepath.Abs(filepath.Dir(manifestPath))
	if err != nil {
		return EvaluatorManifest{}, errors.New("cannot resolve evaluator manifest directory")
	}
	manifestDirectory, err = filepath.EvalSymlinks(manifestDirectory)
	if err != nil {
		return EvaluatorManifest{}, errors.New("cannot resolve evaluator manifest directory")
	}
	for index := range manifest.Evaluators {
		definition := &manifest.Evaluators[index]
		content, err := readEvaluatorAsset(manifestDirectory, definition.AssetFile)
		if err != nil {
			return EvaluatorManifest{}, fmt.Errorf("evaluator %q asset: %w", definition.Key, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return EvaluatorManifest{}, fmt.Errorf("evaluator %q asset is empty", definition.Key)
		}
		definition.AssetContent = string(content)
		definition.ContentHash, err = evaluatorDefinitionHash(*definition)
		if err != nil {
			return EvaluatorManifest{}, fmt.Errorf("hash evaluator %q: %w", definition.Key, err)
		}
	}
	return manifest, nil
}

func (m EvaluatorManifest) Validate() error {
	if m.Version != EvaluatorManifestVersion {
		return fmt.Errorf("unsupported evaluator manifest version %d", m.Version)
	}
	if len(m.Evaluators) == 0 {
		return errors.New("evaluator manifest must define at least one evaluator")
	}

	seenKeys := make(map[string]struct{}, len(m.Evaluators))
	seenNames := make(map[string]struct{}, len(m.Evaluators))
	for _, definition := range m.Evaluators {
		if err := definition.Validate(); err != nil {
			return fmt.Errorf("evaluator %q: %w", definition.Key, err)
		}
		if _, exists := seenKeys[definition.Key]; exists {
			return fmt.Errorf("duplicate evaluator key %q", definition.Key)
		}
		seenKeys[definition.Key] = struct{}{}
		name := strings.ToLower(strings.TrimSpace(definition.Name))
		if _, exists := seenNames[name]; exists {
			return fmt.Errorf("duplicate evaluator name %q", definition.Name)
		}
		seenNames[name] = struct{}{}
	}
	return nil
}

func (d EvaluatorDefinition) Validate() error {
	if !isEvaluatorKey(d.Key) {
		return errors.New("key must use lowercase letters, numbers, and hyphens")
	}
	name := strings.TrimSpace(d.Name)
	if name == "" {
		return errors.New("name is required")
	}
	if name != d.Name {
		return errors.New("name must not have leading or trailing whitespace")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return errors.New("name cannot contain control characters")
		}
	}
	if d.Type != EvaluatorTypeCode && d.Type != EvaluatorTypePrompt {
		return fmt.Errorf("unsupported evaluator type %q", d.Type)
	}
	version := strings.TrimSpace(d.Version)
	if version != d.Version || !evaluatorVersionPattern.MatchString(version) || strings.EqualFold(version, "latest") {
		return errors.New("version must be a concrete version identifier")
	}
	if !isRelativeAssetPath(d.AssetFile) {
		return errors.New("assetFile must be a normalized relative path within the manifest directory")
	}
	if err := validateEvaluatorSchemas("input", d.InputSchemas); err != nil {
		return err
	}
	if err := validateEvaluatorSchemas("output", d.OutputSchemas); err != nil {
		return err
	}
	return nil
}

func validateEvaluatorSchemas(kind string, schemas []EvaluatorFieldSchema) error {
	if len(schemas) == 0 {
		return fmt.Errorf("%s schemas are required", kind)
	}
	seen := make(map[string]struct{}, len(schemas))
	for _, schema := range schemas {
		key := strings.TrimSpace(schema.Key)
		if key == "" || key != schema.Key {
			return fmt.Errorf("%s schema key is required", kind)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate %s schema key %q", kind, key)
		}
		if kind == "input" {
			if _, allowed := allowedEvaluatorInputFields[key]; !allowed {
				return fmt.Errorf("unsupported evaluator input field %q", key)
			}
		}
		seen[key] = struct{}{}
		switch schema.Type {
		case "string", "number", "integer", "boolean", "array", "object":
		default:
			return fmt.Errorf("unsupported %s schema type %q for key %q", kind, schema.Type, key)
		}
		if (schema.Minimum != nil || schema.Maximum != nil) && schema.Type != "number" && schema.Type != "integer" {
			return fmt.Errorf("%s schema range requires a numeric type for key %q", kind, key)
		}
		if schema.Minimum != nil && schema.Maximum != nil && *schema.Minimum > *schema.Maximum {
			return fmt.Errorf("%s schema minimum exceeds maximum for key %q", kind, key)
		}
	}
	return nil
}

func readEvaluatorAsset(manifestDirectory, assetFile string) ([]byte, error) {
	if !isRelativeAssetPath(assetFile) {
		return nil, errors.New("assetFile must stay within the manifest directory")
	}
	root, err := filepath.EvalSymlinks(manifestDirectory)
	if err != nil {
		return nil, errors.New("cannot resolve evaluator manifest directory")
	}
	assetPath := filepath.Join(root, filepath.FromSlash(assetFile))
	assetPath, err = filepath.EvalSymlinks(assetPath)
	if err != nil {
		return nil, errors.New("cannot resolve evaluator asset path")
	}
	relativePath, err := filepath.Rel(root, assetPath)
	if err != nil || !isWithinDirectory(relativePath) {
		return nil, errors.New("assetFile resolves outside the manifest directory")
	}
	file, err := os.Open(assetPath)
	if err != nil {
		return nil, errors.New("cannot open evaluator asset")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("evaluator asset must be a regular file")
	}
	if info.Size() > maxEvaluatorAssetSize {
		return nil, errors.New("evaluator asset exceeds the size limit")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxEvaluatorAssetSize+1))
	if err != nil {
		return nil, errors.New("cannot read evaluator asset")
	}
	if len(content) > maxEvaluatorAssetSize {
		return nil, errors.New("evaluator asset exceeds the size limit")
	}
	return content, nil
}

func evaluatorDefinitionHash(definition EvaluatorDefinition) (string, error) {
	assetContent := definition.AssetContent
	definition.AssetContent = ""
	definition.ContentHash = ""
	metadata, err := json.Marshal(definition)
	if err != nil {
		return "", errors.New("cannot encode evaluator definition")
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("cozeloop-evaluator-v1\x00"))
	_, _ = hash.Write(metadata)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(assetContent))
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type evaluatorSchemaFingerprint struct {
	Key      string   `json:"key"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Minimum  *float64 `json:"minimum,omitempty"`
	Maximum  *float64 `json:"maximum,omitempty"`
}

// EvaluatorPlatformContentHash fingerprints only the immutable evaluator
// content represented by the public API: type, schemas, runtime/message mode,
// and the code or prompt asset. Local keys, names, descriptions, and versions
// are deliberately excluded because they are evaluator/version metadata.
func EvaluatorPlatformContentHash(definition EvaluatorDefinition) (string, error) {
	if err := definition.Validate(); err != nil {
		return "", fmt.Errorf("invalid evaluator definition: %w", err)
	}
	if strings.TrimSpace(definition.AssetContent) == "" {
		return "", errors.New("evaluator asset content is required")
	}
	inputSchemas := make([]evaluatorSchemaFingerprint, 0, len(definition.InputSchemas))
	for _, schema := range definition.InputSchemas {
		inputSchemas = append(inputSchemas, evaluatorSchemaFingerprint{Key: schema.Key, Type: schema.Type, Required: schema.Required, Minimum: schema.Minimum, Maximum: schema.Maximum})
	}
	outputSchemas := make([]evaluatorSchemaFingerprint, 0, len(definition.OutputSchemas))
	for _, schema := range definition.OutputSchemas {
		outputSchemas = append(outputSchemas, evaluatorSchemaFingerprint{Key: schema.Key, Type: schema.Type, Required: schema.Required, Minimum: schema.Minimum, Maximum: schema.Maximum})
	}
	payload := struct {
		Type               EvaluatorType                `json:"type"`
		ReceiveChatHistory bool                         `json:"receiveChatHistory"`
		InputSchemas       []evaluatorSchemaFingerprint `json:"inputSchemas"`
		OutputSchemas      []evaluatorSchemaFingerprint `json:"outputSchemas"`
		Runtime            string                       `json:"runtime,omitempty"`
		PromptRole         string                       `json:"promptRole,omitempty"`
		PromptSourceType   string                       `json:"promptSourceType,omitempty"`
		AssetContent       string                       `json:"assetContent"`
	}{
		Type:          definition.Type,
		InputSchemas:  inputSchemas,
		OutputSchemas: outputSchemas,
		AssetContent:  definition.AssetContent,
	}
	if definition.Type == EvaluatorTypeCode {
		payload.Runtime = "Python"
	} else {
		payload.PromptRole = "System"
		payload.PromptSourceType = "Custom"
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("cannot encode evaluator platform content")
	}
	hash := sha256.Sum256(append([]byte("cozeloop-evaluator-content-v1\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("unexpected trailing data: %w", err)
	}
	return nil
}

func isEvaluatorKey(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' || value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for _, r := range value {
		isLetter := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != '-' {
			return false
		}
		if r == '-' && previousDash {
			return false
		}
		previousDash = r == '-'
	}
	return true
}

func isRelativeAssetPath(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || strings.Contains(value, `\`) {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return filepath.IsLocal(filepath.FromSlash(value)) && cleaned == value && cleaned != "."
}

func isWithinDirectory(relativePath string) bool {
	return relativePath != "." && !filepath.IsAbs(relativePath) && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}
