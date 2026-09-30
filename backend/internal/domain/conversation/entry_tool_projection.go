package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

type SafeField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type SafeToolCall struct {
	id            string
	name          string
	policyVersion string
	fields        []SafeField
}

func (c SafeToolCall) ID() string            { return c.id }
func (c SafeToolCall) Name() string          { return c.name }
func (c SafeToolCall) PolicyVersion() string { return c.policyVersion }
func (c SafeToolCall) Fields() []SafeField {
	if c.fields == nil {
		return nil
	}
	cp := make([]SafeField, len(c.fields))
	copy(cp, c.fields)
	return cp
}

func (c SafeToolCall) Clone() SafeToolCall {
	return SafeToolCall{
		id:            c.id,
		name:          c.name,
		policyVersion: c.policyVersion,
		fields:        c.Fields(),
	}
}

type safeToolCallDTO struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	PolicyVersion string      `json:"policyVersion"`
	Fields        []SafeField `json:"fields"`
}

func (c SafeToolCall) MarshalJSON() ([]byte, error) {
	fields := c.fields
	if fields == nil {
		fields = []SafeField{}
	}
	return json.Marshal(safeToolCallDTO{
		ID:            c.id,
		Name:          c.name,
		PolicyVersion: c.policyVersion,
		Fields:        fields,
	})
}

func (c *SafeToolCall) UnmarshalJSON(data []byte) error {
	var dto safeToolCallDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dto); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("safe tool call: trailing data after JSON")
	}
	c.id = dto.ID
	c.name = dto.Name
	c.policyVersion = dto.PolicyVersion
	c.fields = dto.Fields
	return nil
}

type SafeToolResult struct {
	policyVersion string
	fields        []SafeField
	preview       string
	truncated     bool
}

func (r SafeToolResult) PolicyVersion() string { return r.policyVersion }
func (r SafeToolResult) Fields() []SafeField {
	if r.fields == nil {
		return nil
	}
	cp := make([]SafeField, len(r.fields))
	copy(cp, r.fields)
	return cp
}
func (r SafeToolResult) Preview() string { return r.preview }
func (r SafeToolResult) Truncated() bool { return r.truncated }

func (r SafeToolResult) Clone() SafeToolResult {
	return SafeToolResult{
		policyVersion: r.policyVersion,
		fields:        r.Fields(),
		preview:       r.preview,
		truncated:     r.truncated,
	}
}

type safeToolResultDTO struct {
	PolicyVersion string      `json:"policyVersion"`
	Fields        []SafeField `json:"fields"`
	Preview       string      `json:"preview"`
	Truncated     bool        `json:"truncated"`
}

func (r SafeToolResult) MarshalJSON() ([]byte, error) {
	fields := r.fields
	if fields == nil {
		fields = []SafeField{}
	}
	return json.Marshal(safeToolResultDTO{
		PolicyVersion: r.policyVersion,
		Fields:        fields,
		Preview:       r.preview,
		Truncated:     r.truncated,
	})
}

func (r *SafeToolResult) UnmarshalJSON(data []byte) error {
	var dto safeToolResultDTO
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dto); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("safe tool result: trailing data after JSON")
	}
	r.policyVersion = dto.PolicyVersion
	r.fields = dto.Fields
	r.preview = dto.Preview
	r.truncated = dto.Truncated
	return nil
}

type ProjectionFieldRule struct {
	SourceKey string
	StoredKey string
	MaxBytes  int
}

type ToolPersistencePolicy struct {
	ToolName        string
	Version         string
	ArgumentFields  []ProjectionFieldRule
	ResultFields    []ProjectionFieldRule
	MaxPreviewBytes int
}

func (p ToolPersistencePolicy) Clone() ToolPersistencePolicy {
	cp := p
	if p.ArgumentFields != nil {
		cp.ArgumentFields = make([]ProjectionFieldRule, len(p.ArgumentFields))
		copy(cp.ArgumentFields, p.ArgumentFields)
	}
	if p.ResultFields != nil {
		cp.ResultFields = make([]ProjectionFieldRule, len(p.ResultFields))
		copy(cp.ResultFields, p.ResultFields)
	}
	return cp
}

type toolPolicyKey struct {
	toolName string
	version  string
}

var (
	sensitiveKeywords = []string{
		"authorization",
		"cookie",
		"secret",
		"token",
		"password",
		"api_key",
		"apikey",
		"private_key",
	}

	policyMu       sync.RWMutex
	policyRegistry = make(map[toolPolicyKey]ToolPersistencePolicy)
)

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, kw := range sensitiveKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func ValidateToolPolicy(policy ToolPersistencePolicy) error {
	if strings.TrimSpace(policy.ToolName) == "" {
		return fmt.Errorf("%w: tool name cannot be empty", domainerr.ErrInvalidInput)
	}
	if strings.TrimSpace(policy.Version) == "" {
		return fmt.Errorf("%w: policy version cannot be empty", domainerr.ErrInvalidInput)
	}
	if policy.MaxPreviewBytes < 0 {
		return fmt.Errorf("%w: max preview bytes cannot be negative", domainerr.ErrInvalidInput)
	}

	checkField := func(field ProjectionFieldRule, kind string) error {
		if strings.TrimSpace(field.SourceKey) == "" {
			return fmt.Errorf("%w: %s source key cannot be empty", domainerr.ErrInvalidInput, kind)
		}
		if strings.TrimSpace(field.StoredKey) == "" {
			return fmt.Errorf("%w: %s stored key cannot be empty", domainerr.ErrInvalidInput, kind)
		}
		if field.MaxBytes <= 0 {
			return fmt.Errorf("%w: %s max bytes must be greater than 0", domainerr.ErrInvalidInput, kind)
		}

		if isSensitiveKey(field.SourceKey) || isSensitiveKey(field.StoredKey) {
			return fmt.Errorf("%w: sensitive key forbidden in %s policy", domainerr.ErrInvalidInput, kind)
		}
		return nil
	}

	checkRules := func(rules []ProjectionFieldRule, kind string) error {
		sources := make(map[string]bool)
		stored := make(map[string]bool)
		for _, rule := range rules {
			if err := checkField(rule, kind); err != nil {
				return err
			}
			if sources[rule.SourceKey] || stored[rule.StoredKey] {
				return fmt.Errorf("%w: duplicate source or stored key in %s policy", domainerr.ErrInvalidInput, kind)
			}
			sources[rule.SourceKey] = true
			stored[rule.StoredKey] = true
		}
		return nil
	}
	if err := checkRules(policy.ArgumentFields, "argument"); err != nil {
		return err
	}
	if err := checkRules(policy.ResultFields, "result"); err != nil {
		return err
	}
	return nil
}

func RegisterToolPolicy(policy ToolPersistencePolicy) error {
	cloned := policy.Clone()
	if err := ValidateToolPolicy(cloned); err != nil {
		return err
	}
	key := policyKey(cloned.ToolName, cloned.Version)
	policyMu.Lock()
	defer policyMu.Unlock()
	if _, exists := policyRegistry[key]; exists {
		return fmt.Errorf("%w: policy %s@%s already registered", domainerr.ErrConflict, cloned.ToolName, cloned.Version)
	}
	policyRegistry[key] = cloned
	return nil
}

func GetToolPolicy(toolName, version string) (ToolPersistencePolicy, bool) {
	key := policyKey(toolName, version)
	policyMu.RLock()
	defer policyMu.RUnlock()
	p, ok := policyRegistry[key]
	if !ok {
		return ToolPersistencePolicy{}, false
	}
	return p.Clone(), true
}

func ClearToolPolicies() {
	policyMu.Lock()
	defer policyMu.Unlock()
	policyRegistry = make(map[toolPolicyKey]ToolPersistencePolicy)
}

func policyKey(toolName, version string) toolPolicyKey {
	return toolPolicyKey{toolName: toolName, version: version}
}

func ValidateSafeToolCall(tc SafeToolCall) error {
	if strings.TrimSpace(tc.id) == "" {
		return fmt.Errorf("%w: tool call ID cannot be empty", domainerr.ErrInvalidInput)
	}
	if strings.TrimSpace(tc.name) == "" {
		return fmt.Errorf("%w: tool call name cannot be empty", domainerr.ErrInvalidInput)
	}
	if strings.TrimSpace(tc.policyVersion) == "" {
		return fmt.Errorf("%w: tool call policy version cannot be empty", domainerr.ErrInvalidInput)
	}
	policy, ok := GetToolPolicy(tc.name, tc.policyVersion)
	if !ok {
		return fmt.Errorf("%w: unregistered tool policy %s@%s", domainerr.ErrInvalidInput, tc.name, tc.policyVersion)
	}

	allowedFields := make(map[string]ProjectionFieldRule)
	fieldOrder := make(map[string]int)
	for i, rule := range policy.ArgumentFields {
		allowedFields[rule.StoredKey] = rule
		fieldOrder[rule.StoredKey] = i
	}

	lastField := -1
	seenKeys := make(map[string]bool)
	for _, f := range tc.fields {
		if isSensitiveKey(f.Name) {
			return fmt.Errorf("%w: sensitive field %q forbidden in tool call", domainerr.ErrInvalidInput, f.Name)
		}
		rule, allowed := allowedFields[f.Name]
		if !allowed {
			return fmt.Errorf("%w: unapproved argument field %q for tool %s@%s", domainerr.ErrInvalidInput, f.Name, tc.name, tc.policyVersion)
		}
		if seenKeys[f.Name] {
			return fmt.Errorf("%w: duplicate argument field %q in tool call", domainerr.ErrInvalidInput, f.Name)
		}
		seenKeys[f.Name] = true
		if fieldOrder[f.Name] <= lastField {
			return fmt.Errorf("%w: argument fields must follow policy declaration order", domainerr.ErrInvalidInput)
		}
		lastField = fieldOrder[f.Name]
		if !utf8.ValidString(f.Value) {
			return fmt.Errorf("%w: argument field %q contains invalid UTF-8", domainerr.ErrInvalidInput, f.Name)
		}
		if len(f.Value) > rule.MaxBytes {
			return NewEntryError(
				EntryErrCodeCapacity,
				"",
				fmt.Sprintf("argument field %s length %d exceeds max bytes %d", f.Name, len(f.Value), rule.MaxBytes),
				ErrEntryCapacity,
			)
		}
	}
	return nil
}

func ValidateSafeToolResult(toolName string, tr SafeToolResult) error {
	if strings.TrimSpace(tr.policyVersion) == "" {
		return fmt.Errorf("%w: tool result policy version cannot be empty", domainerr.ErrInvalidInput)
	}
	policy, ok := GetToolPolicy(toolName, tr.policyVersion)
	if !ok {
		return fmt.Errorf("%w: unregistered tool policy %s@%s", domainerr.ErrInvalidInput, toolName, tr.policyVersion)
	}

	allowedFields := make(map[string]ProjectionFieldRule)
	fieldOrder := make(map[string]int)
	for i, rule := range policy.ResultFields {
		allowedFields[rule.StoredKey] = rule
		fieldOrder[rule.StoredKey] = i
	}

	lastField := -1
	seenKeys := make(map[string]bool)
	for _, f := range tr.fields {
		if isSensitiveKey(f.Name) {
			return fmt.Errorf("%w: sensitive field %q forbidden in tool result", domainerr.ErrInvalidInput, f.Name)
		}
		rule, allowed := allowedFields[f.Name]
		if !allowed {
			return fmt.Errorf("%w: unapproved result field %q for tool %s@%s", domainerr.ErrInvalidInput, f.Name, toolName, tr.policyVersion)
		}
		if seenKeys[f.Name] {
			return fmt.Errorf("%w: duplicate result field %q in tool result", domainerr.ErrInvalidInput, f.Name)
		}
		seenKeys[f.Name] = true
		if fieldOrder[f.Name] <= lastField {
			return fmt.Errorf("%w: result fields must follow policy declaration order", domainerr.ErrInvalidInput)
		}
		lastField = fieldOrder[f.Name]
		if !utf8.ValidString(f.Value) {
			return fmt.Errorf("%w: result field %q contains invalid UTF-8", domainerr.ErrInvalidInput, f.Name)
		}
		if len(f.Value) > rule.MaxBytes {
			return fmt.Errorf("%w: result field %s length %d exceeds max bytes %d", domainerr.ErrInvalidInput, f.Name, len(f.Value), rule.MaxBytes)
		}
	}

	if policy.MaxPreviewBytes > 0 && len(tr.preview) > policy.MaxPreviewBytes {
		return fmt.Errorf("%w: preview length %d exceeds max preview bytes %d", domainerr.ErrInvalidInput, len(tr.preview), policy.MaxPreviewBytes)
	}
	preview, previewTruncated := projectedResultPreview(tr.fields, policy.MaxPreviewBytes)
	if tr.preview != preview {
		return fmt.Errorf("%w: preview must match approved projected fields", domainerr.ErrInvalidInput)
	}
	if previewTruncated && !tr.truncated {
		return fmt.Errorf("%w: truncated preview must be marked as truncated", domainerr.ErrInvalidInput)
	}
	return nil
}

// projectedResultPreview derives the preview only from fields in policy declaration order.
// A zero limit preserves the existing unbounded preview semantics.
func projectedResultPreview(fields []SafeField, maxBytes int) (string, bool) {
	var preview string
	for _, f := range fields {
		if preview == "" && (strings.EqualFold(f.Name, "preview") || strings.EqualFold(f.Name, "summary")) {
			preview = f.Value
		}
	}
	if preview == "" {
		var parts []string
		for _, f := range fields {
			parts = append(parts, f.Name+"="+f.Value)
		}
		preview = strings.Join(parts, "; ")
	}
	if maxBytes > 0 {
		return truncateUTF8(preview, maxBytes)
	}
	return preview, false
}

func truncateUTF8(s string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	idx := maxBytes
	for idx > 0 && !utf8.RuneStart(s[idx]) {
		idx--
	}
	return s[:idx], true
}

func ProjectSafeToolCall(
	policy ToolPersistencePolicy,
	callID string,
	rawArguments json.RawMessage,
) (SafeToolCall, error) {
	if strings.TrimSpace(callID) == "" {
		return SafeToolCall{}, fmt.Errorf("%w: call ID cannot be empty", domainerr.ErrInvalidInput)
	}
	registered, ok := GetToolPolicy(policy.ToolName, policy.Version)
	if !ok {
		return SafeToolCall{}, fmt.Errorf("%w: unregistered tool policy %s@%s", domainerr.ErrInvalidInput, policy.ToolName, policy.Version)
	}
	policy = registered

	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(rawArguments))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return SafeToolCall{}, fmt.Errorf("%w: invalid raw arguments json: %v", domainerr.ErrInvalidInput, err)
	}

	fields := make([]SafeField, 0, len(policy.ArgumentFields))
	for _, rule := range policy.ArgumentFields {
		val, exists := obj[rule.SourceKey]
		if !exists || val == nil {
			continue
		}
		strVal, err := scalarToString(val)
		if err != nil {
			return SafeToolCall{}, fmt.Errorf("%w: field %s is not scalar: %v", domainerr.ErrInvalidInput, rule.SourceKey, err)
		}
		if len(strVal) > rule.MaxBytes {
			return SafeToolCall{}, NewEntryError(
				EntryErrCodeCapacity,
				"",
				fmt.Sprintf("argument field %s length %d exceeds max bytes %d", rule.SourceKey, len(strVal), rule.MaxBytes),
				ErrEntryCapacity,
			)
		}
		fields = append(fields, SafeField{
			Name:  rule.StoredKey,
			Value: strVal,
		})
	}

	return SafeToolCall{
		id:            callID,
		name:          policy.ToolName,
		policyVersion: policy.Version,
		fields:        fields,
	}, nil
}

func ProjectSafeToolResult(
	policy ToolPersistencePolicy,
	rawResult json.RawMessage,
) (SafeToolResult, error) {
	registered, ok := GetToolPolicy(policy.ToolName, policy.Version)
	if !ok {
		return SafeToolResult{}, fmt.Errorf("%w: unregistered tool policy %s@%s", domainerr.ErrInvalidInput, policy.ToolName, policy.Version)
	}
	policy = registered

	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(rawResult))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil {
		return SafeToolResult{}, fmt.Errorf("%w: invalid raw result json: %v", domainerr.ErrInvalidInput, err)
	}

	fields := make([]SafeField, 0, len(policy.ResultFields))
	truncated := false

	for _, rule := range policy.ResultFields {
		val, exists := obj[rule.SourceKey]
		if !exists || val == nil {
			continue
		}
		strVal, err := scalarToString(val)
		if err != nil {
			return SafeToolResult{}, fmt.Errorf("%w: field %s is not scalar: %v", domainerr.ErrInvalidInput, rule.SourceKey, err)
		}
		if len(strVal) > rule.MaxBytes {
			strVal, _ = truncateUTF8(strVal, rule.MaxBytes)
			truncated = true
		}
		fields = append(fields, SafeField{
			Name:  rule.StoredKey,
			Value: strVal,
		})
	}

	preview, previewTruncated := projectedResultPreview(fields, policy.MaxPreviewBytes)
	truncated = truncated || previewTruncated

	return SafeToolResult{
		policyVersion: policy.Version,
		fields:        fields,
		preview:       preview,
		truncated:     truncated,
	}, nil
}

func scalarToString(val any) (string, error) {
	switch v := val.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	default:
		return "", errors.New("unsupported non-scalar type")
	}
}
