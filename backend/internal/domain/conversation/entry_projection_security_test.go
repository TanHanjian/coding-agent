package conversation_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"interview-memory-agent/backend/internal/domain/conversation"
	"interview-memory-agent/backend/internal/domain/domainerr"
)

func TestProjectionSecurityPreviewInjection(t *testing.T) {
	setupTestPolicies(t)
	for _, fields := range []string{`[{"name":"count","value":"5"}]`, `[]`} {
		t.Run(fields, func(t *testing.T) {
			var result conversation.SafeToolResult
			if err := json.Unmarshal([]byte(`{"policyVersion":"v1","fields":`+fields+`,"preview":"raw credential","truncated":false}`), &result); err != nil {
				t.Fatal(err)
			}
			if err := conversation.ValidateSafeToolResult("search", result); !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Errorf("expected preview injection rejection, got %v", err)
			}
			message := conversation.EntryMessage{
				Role:       conversation.EntryRoleToolResult,
				ToolResult: &conversation.EntryToolResult{CallEntryID: "e1", CallID: "c1", ToolName: "search", Result: result},
			}
			if err := conversation.ValidateEntryMessage(message); !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Errorf("entry accepted injected preview: %v", err)
			}
		})
	}
}

func TestProjectionSecurityPreviewTruncationMetadata(t *testing.T) {
	conversation.ClearToolPolicies()
	t.Cleanup(conversation.ClearToolPolicies)
	if err := conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
		ToolName: "truncation", Version: "v1", MaxPreviewBytes: 4,
		ResultFields: []conversation.ProjectionFieldRule{{SourceKey: "summary", StoredKey: "summary", MaxBytes: 100}},
	}); err != nil {
		t.Fatal(err)
	}
	projected := mustProjectResult(t, "truncation", "v1", `{"summary":"abcdef"}`)
	if projected.Preview() != "abcd" || !projected.Truncated() {
		t.Fatal("projector did not mark preview truncation")
	}
	var forged conversation.SafeToolResult
	if err := json.Unmarshal([]byte(`{"policyVersion":"v1","fields":[{"name":"summary","value":"abcdef"}],"preview":"abcd","truncated":false}`), &forged); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolResult("truncation", forged); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("unmarked preview truncation accepted: %v", err)
	}
	if err := conversation.ValidateSafeToolResult("truncation", projected); err != nil {
		t.Fatalf("valid truncated result rejected: %v", err)
	}
	// A true flag must remain valid when only original field data was truncated;
	// that information cannot be reconstructed from the stored field alone.
	if err := json.Unmarshal([]byte(`{"policyVersion":"v1","fields":[{"name":"summary","value":"abc"}],"preview":"abc","truncated":true}`), &forged); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolResult("truncation", forged); err != nil {
		t.Fatalf("field-level truncation flag rejected: %v", err)
	}
}

func TestProjectionSecurityInvalidSnapshots(t *testing.T) {
	setupTestPolicies(t)
	for _, tc := range []struct {
		name, tool, version, fields, preview string
	}{
		{"unregistered", "missing", "v1", `[]`, ""},
		{"empty version", "search", "", `[]`, ""},
		{"unauthorized", "search", "v1", `[{"name":"raw","value":"credential"}]`, "raw=credential"},
		{"sensitive", "search", "v1", `[{"name":"Authorization","value":"credential"}]`, ""},
		{"duplicate", "search", "v1", `[{"name":"count","value":"5"},{"name":"count","value":"6"}]`, "count=5; count=6"},
		{"overlong UTF8", "search", "v1", `[{"name":"summary","value":"` + strings.Repeat("界", 34) + `"}]`, ""},
		{"reordered", "search", "v1", `[{"name":"summary","value":"ok"},{"name":"count","value":"5"}]`, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{"policyVersion": tc.version, "fields": json.RawMessage(tc.fields), "preview": tc.preview})
			if err != nil {
				t.Fatal(err)
			}
			var result conversation.SafeToolResult
			if err := json.Unmarshal(data, &result); err != nil {
				t.Fatal(err)
			}
			if err := conversation.ValidateSafeToolResult(tc.tool, result); !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Fatalf("expected invalid snapshot, got %v", err)
			}
		})
	}
	var call conversation.SafeToolCall
	if err := json.Unmarshal([]byte(`{"id":"c1","name":"search","policyVersion":"v1","fields":[{"name":"q","value":"`+strings.Repeat("界", 34)+`"}]}`), &call); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolCall(call); !errors.Is(err, conversation.ErrEntryCapacity) {
		t.Fatalf("expected argument byte capacity error, got %v", err)
	}
}

func TestProjectionSecurityEmptyAllowlist(t *testing.T) {
	conversation.ClearToolPolicies()
	policy := conversation.ToolPersistencePolicy{ToolName: "empty", Version: "v1"}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	result := mustProjectResult(t, "empty", "v1", `{"raw":"credential"}`)
	if len(result.Fields()) != 0 || result.Preview() != "" {
		t.Fatal("empty allowlist projected raw data")
	}
	if err := conversation.ValidateSafeToolResult("empty", result); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		`{"policyVersion":"v1","fields":[],"preview":"raw credential"}`,
		`{"policyVersion":"v1","fields":[{"name":"raw","value":"credential"}],"preview":"raw=credential"}`,
	} {
		if err := json.Unmarshal([]byte(data), &result); err != nil {
			t.Fatal(err)
		}
		if err := conversation.ValidateSafeToolResult("empty", result); !errors.Is(err, domainerr.ErrInvalidInput) {
			t.Fatalf("empty allowlist accepted forged data: %v", err)
		}
	}
}

func TestProjectionSecurityDuplicateRules(t *testing.T) {
	for _, kind := range []string{"argument", "result"} {
		for _, key := range []string{"source", "stored"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				conversation.ClearToolPolicies()
				rules := []conversation.ProjectionFieldRule{
					{SourceKey: "a", StoredKey: "first", MaxBytes: 10},
					{SourceKey: "b", StoredKey: "second", MaxBytes: 20},
				}
				if key == "source" {
					rules[1].SourceKey = rules[0].SourceKey
				} else {
					rules[1].StoredKey = rules[0].StoredKey
				}
				policy := conversation.ToolPersistencePolicy{ToolName: "duplicates", Version: "v1"}
				if kind == "argument" {
					policy.ArgumentFields = rules
				} else {
					policy.ResultFields = rules
				}
				if err := conversation.ValidateToolPolicy(policy); !errors.Is(err, domainerr.ErrInvalidInput) {
					t.Errorf("ambiguous rules validated: %v", err)
				}
				if err := conversation.RegisterToolPolicy(policy); !errors.Is(err, domainerr.ErrInvalidInput) {
					t.Errorf("ambiguous rules registered: %v", err)
				}
			})
		}
	}
}

func TestProjectionSecurityRegistryKeyCollision(t *testing.T) {
	conversation.ClearToolPolicies()
	policy := conversation.ToolPersistencePolicy{ToolName: "tool@v1", Version: "v2"}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, ok := conversation.GetToolPolicy("tool", "v1@v2"); ok {
		t.Error("unregistered tuple resolved through concatenation collision")
	}
	var result conversation.SafeToolResult
	if err := json.Unmarshal([]byte(`{"policyVersion":"v1@v2","fields":[],"preview":""}`), &result); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolResult("tool", result); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Errorf("unregistered tuple validated: %v", err)
	}
	other := conversation.ToolPersistencePolicy{ToolName: "tool", Version: "v1@v2"}
	if err := conversation.RegisterToolPolicy(other); err != nil {
		t.Fatalf("distinct tuple should register: %v", err)
	}
}

func TestProjectionSecurityImmutabilityAndOrder(t *testing.T) {
	conversation.ClearToolPolicies()
	policy := conversation.ToolPersistencePolicy{
		ToolName: "ordered", Version: "v1", MaxPreviewBytes: 100,
		ArgumentFields: []conversation.ProjectionFieldRule{
			{SourceKey: "a", StoredKey: "first", MaxBytes: 10},
			{SourceKey: "b", StoredKey: "second", MaxBytes: 10},
		},
		ResultFields: []conversation.ProjectionFieldRule{
			{SourceKey: "a", StoredKey: "first", MaxBytes: 10},
			{SourceKey: "b", StoredKey: "second", MaxBytes: 10},
		},
	}
	if err := conversation.RegisterToolPolicy(policy); err != nil {
		t.Fatal(err)
	}
	policy.ArgumentFields[0] = conversation.ProjectionFieldRule{SourceKey: "authorization", StoredKey: "raw", MaxBytes: 1000}
	policy.ResultFields[0] = policy.ArgumentFields[0]
	returned, _ := conversation.GetToolPolicy("ordered", "v1")
	returned.ArgumentFields[0] = policy.ArgumentFields[0]
	returned.ResultFields[0] = policy.ResultFields[0]

	call := mustProjectCall(t, "ordered", "v1", "c1", `{"b":"2","authorization":"raw credential","a":"1"}`)
	result := mustProjectResult(t, "ordered", "v1", `{"b":"2","authorization":"raw credential","a":"1"}`)
	want := []conversation.SafeField{{Name: "first", Value: "1"}, {Name: "second", Value: "2"}}
	call.Fields()[0].Value = "tampered"
	result.Fields()[0].Value = "tampered"
	call.Clone().Fields()[0].Name = "tampered"
	result.Clone().Fields()[0].Name = "tampered"
	if !reflect.DeepEqual(call.Fields(), want) || !reflect.DeepEqual(result.Fields(), want) {
		t.Fatalf("mutated or unordered snapshots: call=%v result=%v", call.Fields(), result.Fields())
	}
	if result.Preview() != "first=1; second=2" {
		t.Fatalf("unexpected preview: %q", result.Preview())
	}
	if err := conversation.ValidateSafeToolCall(call); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolResult("ordered", result); err != nil {
		t.Fatal(err)
	}
	var reordered conversation.SafeToolCall
	if err := json.Unmarshal([]byte(`{"id":"c1","name":"ordered","policyVersion":"v1","fields":[{"name":"second","value":"2"},{"name":"first","value":"1"}]}`), &reordered); err != nil {
		t.Fatal(err)
	}
	if err := conversation.ValidateSafeToolCall(reordered); !errors.Is(err, domainerr.ErrInvalidInput) {
		t.Fatalf("reordered call validated: %v", err)
	}
}

func TestProjectionSecurityRoundtripCanonicalPreview(t *testing.T) {
	for _, tc := range []struct {
		name, raw, preview string
		maxPreview         int
		truncated          bool
	}{
		{"summary", `{"count":5,"summary":"界界界界"}`, "界界", 7, true},
		{"fallback", `{"count":5}`, "count=5", 100, false},
		{"case insensitive preview", `{"detail":"ok"}`, "ok", 100, false},
		{"summary takes declaration precedence", `{"detail":"second","summary":"first"}`, "first", 100, false},
		{"empty summary uses next preview", `{"detail":"next","summary":""}`, "next", 100, false},
		{"empty summary fallback", `{"summary":"","count":5}`, "count=5; summary=", 100, false},
		{"no fields", `{}`, "", 100, false},
		{"zero means unbounded", `{"summary":"界界界界"}`, "界界界", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversation.ClearToolPolicies()
			policy := conversation.ToolPersistencePolicy{
				ToolName: "roundtrip", Version: "v1", MaxPreviewBytes: tc.maxPreview,
				ArgumentFields: []conversation.ProjectionFieldRule{{SourceKey: "q", StoredKey: "q", MaxBytes: 10}},
				ResultFields: []conversation.ProjectionFieldRule{
					{SourceKey: "count", StoredKey: "count", MaxBytes: 10},
					{SourceKey: "summary", StoredKey: "summary", MaxBytes: 10},
					{SourceKey: "detail", StoredKey: "Preview", MaxBytes: 10},
				},
			}
			if err := conversation.RegisterToolPolicy(policy); err != nil {
				t.Fatal(err)
			}
			call := mustProjectCall(t, "roundtrip", "v1", "c1", `{"q":"界"}`)
			result := mustProjectResult(t, "roundtrip", "v1", tc.raw)
			if result.Preview() != tc.preview || result.Truncated() != tc.truncated || !utf8.ValidString(result.Preview()) {
				t.Fatalf("unexpected preview/truncation: %q, %v", result.Preview(), result.Truncated())
			}
			for _, field := range result.Fields() {
				if !utf8.ValidString(field.Value) {
					t.Fatalf("invalid UTF8 field: %q", field.Value)
				}
			}
			callJSON, err := json.Marshal(call)
			if err != nil {
				t.Fatal(err)
			}
			resultJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var callCopy conversation.SafeToolCall
			var resultCopy conversation.SafeToolResult
			if err := json.Unmarshal(callJSON, &callCopy); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(resultJSON, &resultCopy); err != nil {
				t.Fatal(err)
			}
			if err := conversation.ValidateSafeToolCall(callCopy); err != nil {
				t.Fatal(err)
			}
			if err := conversation.ValidateSafeToolResult("roundtrip", resultCopy); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(call.Fields(), callCopy.Fields()) || !reflect.DeepEqual(result.Fields(), resultCopy.Fields()) || resultCopy.Preview() != tc.preview || resultCopy.Truncated() != tc.truncated {
				t.Fatal("JSON roundtrip changed snapshot")
			}
			// Even an unlimited preview must be derived from projected fields.
			var forged map[string]any
			if err := json.Unmarshal(resultJSON, &forged); err != nil {
				t.Fatal(err)
			}
			forged["preview"] = "raw credential"
			forgedJSON, err := json.Marshal(forged)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(forgedJSON, &resultCopy); err != nil {
				t.Fatal(err)
			}
			if err := conversation.ValidateSafeToolResult("roundtrip", resultCopy); !errors.Is(err, domainerr.ErrInvalidInput) {
				t.Fatalf("forged preview validated: %v", err)
			}
		})
	}
}
