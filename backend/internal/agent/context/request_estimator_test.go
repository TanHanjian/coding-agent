package agentcontext

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

func requestTestSchema(t *testing.T, text string) *jsonschema.Schema {
	t.Helper()
	var result jsonschema.Schema
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func requestTestCount(t *testing.T, messages []*schema.Message, tools []*schema.ToolInfo) int {
	t.Helper()
	count, err := NewRequestTokenEstimator().CountMessages("test-model", messages, tools)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRequestEstimatorCompleteRepresentation(t *testing.T) {
	// Independent JSON oracle: do not use the implementation's wire structs.
	params := requestTestSchema(t, `{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"$defs":{"entry":{"type":"object","properties":{"value":{"type":"string","enum":["中文","quoted\"value"]}},"required":["value"]}},
		"type":"object","properties":{"items":{"type":"array","items":{"$ref":"#/$defs/entry"}},"choice":{"anyOf":[{"type":"string"},{"type":"null"}]}},
		"required":["items","choice"],"additionalProperties":false
	}`)
	index := 2
	messages := []*schema.Message{
		{Role: schema.System, Content: "instruction"},
		{Role: schema.User, Content: "正文\n\"<&中文", Name: "user-name"},
		{Role: schema.Assistant, ReasoningContent: "reasoning", ToolCalls: []schema.ToolCall{{
			Index: &index, ID: "call-id", Type: "ignored-type", Function: schema.FunctionCall{Name: "lookup", Arguments: `{"items":[{"value":"中文"}]}`},
		}}},
		{Role: schema.Tool, Content: "tool-result", Name: "lookup", ToolCallID: "call-id"},
	}
	tools := []*schema.ToolInfo{{Name: "lookup", Desc: "complete schema", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)}}
	var expected any
	if err := json.Unmarshal([]byte(`{
		"messages":[
			{"role":"system","content":"instruction"},
			{"role":"user","content":"正文\n\"<&中文","name":"user-name"},
			{"role":"assistant","content":"","reasoning_content":"reasoning","tool_calls":[{"index":2,"id":"call-id","type":"function","function":{"name":"lookup","arguments":"{\"items\":[{\"value\":\"中文\"}]}"}}]},
			{"role":"tool","content":"tool-result","name":"lookup","tool_call_id":"call-id"}
		],
		"tools":[{"type":"function","function":{"name":"lookup","description":"complete schema","parameters":null}}]
	}`), &expected); err != nil {
		t.Fatal(err)
	}
	encodedParams, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var expectedParams any
	if err := json.Unmarshal(encodedParams, &expectedParams); err != nil {
		t.Fatal(err)
	}
	expected.(map[string]any)["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"] = expectedParams
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestTestCount(t, messages, tools); got != len(encoded) {
		t.Fatalf("complete request count = %d, want JSON bytes %d", got, len(encoded))
	}
}

func TestRequestEstimatorFieldGrowth(t *testing.T) {
	padding := strings.Repeat("x", 1024)
	cases := []struct {
		name string
		grow func(*schema.Message, *schema.ToolInfo, *jsonschema.Schema)
	}{
		{"input body", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) { m.Content += padding }},
		{"message name", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) { m.Name += padding }},
		{"tool result ID", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) { m.ToolCallID += padding }},
		{"reasoning", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) { m.ReasoningContent += padding }},
		{"call ID", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) { m.ToolCalls[0].ID += padding }},
		{"function name", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) {
			m.ToolCalls[0].Function.Name += padding
		}},
		{"arguments", func(m *schema.Message, _ *schema.ToolInfo, _ *jsonschema.Schema) {
			m.ToolCalls[0].Function.Arguments += padding
		}},
		{"tool name", func(_ *schema.Message, info *schema.ToolInfo, _ *jsonschema.Schema) { info.Name += padding }},
		{"tool description", func(_ *schema.Message, info *schema.ToolInfo, _ *jsonschema.Schema) { info.Desc += padding }},
		{"nested schema", func(_ *schema.Message, _ *schema.ToolInfo, s *jsonschema.Schema) {
			s.Definitions["entry"].Description += padding
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := requestTestSchema(t, `{"type":"object","$defs":{"entry":{"type":"string","description":"entry"}},"properties":{"value":{"$ref":"#/$defs/entry"}}}`)
			m := &schema.Message{Role: schema.Assistant, Content: "a", Name: "b", ToolCallID: "c", ReasoningContent: "d", ToolCalls: []schema.ToolCall{{ID: "e", Function: schema.FunctionCall{Name: "f", Arguments: "g"}}}}
			info := &schema.ToolInfo{Name: "h", Desc: "i", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)}
			before := requestTestCount(t, []*schema.Message{m}, []*schema.ToolInfo{info})
			tc.grow(m, info, params)
			after := requestTestCount(t, []*schema.Message{m}, []*schema.ToolInfo{info})
			if after-before != len(padding) {
				t.Fatalf("growth = %d, want %d", after-before, len(padding))
			}
		})
	}
	t.Run("message and tool structure", func(t *testing.T) {
		messages := []*schema.Message{schema.UserMessage("input")}
		base := requestTestCount(t, messages, nil)
		if got := requestTestCount(t, append(messages, schema.AssistantMessage("", nil)), nil); got <= base {
			t.Fatalf("adding a message did not increase count: %d <= %d", got, base)
		}
		if got := requestTestCount(t, messages, []*schema.ToolInfo{{Name: "no-params"}}); got <= base {
			t.Fatalf("adding a tool did not increase count: %d <= %d", got, base)
		}
	})
}

func TestRequestEstimatorUTF8AndJSONEscaping(t *testing.T) {
	estimator := NewRequestTokenEstimator()
	for _, text := range []string{"", "ascii", "中文🙂", "\"\\\n<&"} {
		got, err := estimator.CountText("ignored", text)
		if err != nil || got != len(text) {
			t.Fatalf("CountText(%q) = %d, %v; want %d", text, got, err, len(text))
		}
		encodedText, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		want := len(`{"messages":[{"role":"user","content":`) + len(encodedText) + len(`}]}`)
		if got := requestTestCount(t, []*schema.Message{schema.UserMessage(text)}, nil); got != want {
			t.Fatalf("CountMessages(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestRequestEstimatorExcludesMetadata(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant, Content: "input", ToolCalls: []schema.ToolCall{{ID: "id", Function: schema.FunctionCall{Name: "tool", Arguments: "{}"}}}}
	info := &schema.ToolInfo{Name: "tool"}
	before := requestTestCount(t, []*schema.Message{m}, []*schema.ToolInfo{info})
	// Unencodable metadata proves exclusion, not just a small contribution.
	m.Extra = map[string]any{"secret": strings.Repeat("private", 1000), "unencodable": make(chan int)}
	m.ToolCalls[0].Extra = map[string]any{"unencodable": func() {}}
	m.ToolCalls[0].Type = strings.Repeat("ignored", 1000)
	m.ToolName = "adapter-does-not-send-this"
	m.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{TotalTokens: 999999}}
	info.Extra = map[string]any{"unencodable": make(chan int)}
	if got := requestTestCount(t, []*schema.Message{m}, []*schema.ToolInfo{info}); got != before {
		t.Fatalf("metadata changed count: %d -> %d", before, got)
	}
}

func TestRequestEstimatorRejectsInvalidRequestsSafely(t *testing.T) {
	const secret = "PRIVATE-REQUEST-CONTENT"
	cases := []struct {
		name     string
		messages []*schema.Message
		tools    []*schema.ToolInfo
		want     string
	}{
		{"nil messages", nil, nil, "request messages are required"},
		{"empty messages", []*schema.Message{}, nil, "request messages are required"},
		{"nil message", []*schema.Message{schema.UserMessage(secret), nil}, nil, "request message is nil"},
		{"empty role", []*schema.Message{{Content: secret}}, nil, "request message role is unsupported"},
		{"unsupported role", []*schema.Message{{Role: schema.RoleType(secret), Content: secret}}, nil, "request message role is unsupported"},
		{"legacy multimodal", []*schema.Message{{Role: schema.User, Content: secret, MultiContent: []schema.ChatMessagePart{{Type: schema.ChatMessagePartTypeImageURL}}}}, nil, "request counting only supports text messages"},
		{"user multimodal", []*schema.Message{{Role: schema.User, Content: secret, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeImageURL}}}}, nil, "request counting only supports text messages"},
		{"assistant multimodal", []*schema.Message{{Role: schema.Assistant, Content: secret, AssistantGenMultiContent: []schema.MessageOutputPart{{Type: schema.ChatMessagePartTypeAudioURL}}}}, nil, "request counting only supports text messages"},
		{"text in multipart", []*schema.Message{{Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeText, Text: secret}}}}, nil, "request counting only supports text messages"},
		{"nil tool", []*schema.Message{schema.UserMessage(secret)}, []*schema.ToolInfo{nil}, "request tool definition is nil"},
		{"unencodable schema", []*schema.Message{schema.UserMessage(secret)}, []*schema.ToolInfo{{Name: secret, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{Default: requestTestMarshalFailure{}})}}, "request representation cannot be encoded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			count, err := NewRequestTokenEstimator().CountMessages(secret, tc.messages, tc.tools)
			if err == nil || err.Error() != tc.want || count != 0 {
				t.Fatalf("got count=%d err=%v; want zero and %q", count, err, tc.want)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("error leaked input")
			}
		})
	}
}

type requestTestMarshalFailure struct{}

func (requestTestMarshalFailure) MarshalJSON() ([]byte, error) {
	return nil, errors.New("PRIVATE-REQUEST-CONTENT: underlying schema encoder detail")
}

func TestSnapshotToolInfosDeepCopy(t *testing.T) {
	params := requestTestSchema(t, `{"type":"object","properties":{"nested":{"type":"array","items":{"type":"string","enum":["a","b"]}}},"required":["nested"],"$defs":{"entry":{"type":"string"}},"anyOf":[{"type":"object"}],"default":{"nested":["initial"]}}`)
	// The dependency's UnmarshalJSON ignores unknown keywords. Construct Extras
	// directly so the fixture actually contains the field the snapshot must copy.
	params.Extras = map[string]any{"x-private": map[string]any{"values": []any{"initial"}}}
	original := &schema.ToolInfo{Name: "lookup", Desc: "description", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params), Extra: map[string]any{"ignored": true}}
	frozen, err := SnapshotToolInfos([]*schema.ToolInfo{original})
	if err != nil || len(frozen) != 1 {
		t.Fatalf("snapshot = %v, %v", frozen, err)
	}
	copyParams, err := frozen[0].ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(copyParams)
	if err != nil {
		t.Fatal(err)
	}
	originalBytes, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(originalBytes) {
		// Report field loss while still exercising isolation of standard fields.
		t.Errorf("snapshot lost schema fields: got %s want %s", before, originalBytes)
	}
	if frozen[0] == original || copyParams == params || frozen[0].Extra != nil {
		t.Fatal("snapshot retained original identity or ignored Extra")
	}
	original.Name, original.Desc = "changed", "changed"
	params.Required[0] = "changed"
	nested, _ := params.Properties.Get("nested")
	nested.Items.Enum[0] = "changed"
	params.Definitions["entry"].Type = "number"
	params.AnyOf[0].Type = "string"
	params.Default.(map[string]any)["nested"].([]any)[0] = "changed"
	params.Extras["x-private"].(map[string]any)["values"].([]any)[0] = "changed"
	after, err := json.Marshal(copyParams)
	if err != nil || string(after) != string(before) || frozen[0].Name != "lookup" || frozen[0].Desc != "description" {
		t.Fatalf("original mutation reached snapshot: %s, err=%v", after, err)
	}
	// Verify isolation in the reverse direction as well.
	copyNested, _ := copyParams.Properties.Get("nested")
	copyNested.Items.Enum[1] = "snapshot-only"
	if nested.Items.Enum[1] != "b" {
		t.Fatal("snapshot mutation reached original")
	}
	// Replacing the caller's slice entry must not replace the snapshot.
	infos := []*schema.ToolInfo{original}
	second, err := SnapshotToolInfos(infos)
	if err != nil {
		t.Fatal(err)
	}
	infos[0] = nil
	if second[0] == nil {
		t.Fatal("snapshot shares caller slice")
	}
}

func TestSnapshotToolInfosParameterMapAndNoParameters(t *testing.T) {
	parameter := &schema.ParameterInfo{Type: schema.String, Desc: "original", Enum: []string{"one"}, Required: true}
	infos := []*schema.ToolInfo{
		{Name: "mapped", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"value": parameter})},
		{Name: "no-params"},
		{Name: "nil-schema", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(nil)},
	}
	frozen, err := SnapshotToolInfos(infos)
	if err != nil {
		t.Fatal(err)
	}
	parameter.Desc, parameter.Enum[0] = "changed", "changed"
	params, err := frozen[0].ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	value, ok := params.Properties.Get("value")
	if !ok || value.Description != "original" || value.Enum[0] != "one" || len(params.Required) != 1 || params.Required[0] != "value" {
		t.Fatalf("parameter map snapshot is not frozen: %+v", params)
	}
	for _, info := range frozen[1:] {
		params, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil || params != nil {
			t.Fatalf("no-parameter tool = %v, %v", params, err)
		}
	}
	requestTestCount(t, []*schema.Message{schema.UserMessage("input")}, frozen)
	for _, infos := range [][]*schema.ToolInfo{nil, {}} {
		got, err := SnapshotToolInfos(infos)
		if err != nil || len(got) != 0 {
			t.Fatalf("empty snapshot = %v, %v", got, err)
		}
	}
}

func TestSnapshotToolInfosErrorsAreSafeAndAtomic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invalid *schema.ToolInfo
		want    string
	}{
		{"nil tool", nil, "tool definition is nil"},
		{"encoder error", &schema.ToolInfo{Name: "PRIVATE-REQUEST-CONTENT", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{Default: requestTestMarshalFailure{}})}, "tool schema cannot be encoded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SnapshotToolInfos([]*schema.ToolInfo{{Name: "valid"}, tc.invalid})
			if got != nil || err == nil || err.Error() != tc.want {
				t.Fatalf("snapshot = %v, %v; want nil and %q", got, err, tc.want)
			}
		})
	}
}

func TestRequestEstimatorRejectsNilParameters(t *testing.T) {
	for _, operation := range []string{"count", "snapshot"} {
		t.Run(operation, func(t *testing.T) {
			infos := []*schema.ToolInfo{{ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"invalid": nil})}}
			if operation == "count" {
				count, err := NewRequestTokenEstimator().CountMessages("test", []*schema.Message{schema.UserMessage("input")}, infos)
				if count != 0 || err == nil {
					t.Fatalf("invalid schema allowed: %d, %v", count, err)
				}
			} else {
				frozen, err := SnapshotToolInfos(infos)
				if frozen != nil || err == nil {
					t.Fatalf("invalid schema copied: %v, %v", frozen, err)
				}
			}
		})
	}
}
