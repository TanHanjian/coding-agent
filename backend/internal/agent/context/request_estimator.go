package agentcontext

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// RequestTokenEstimator counts UTF-8 bytes of the text request representation.
// It deliberately overestimates common tokenizers, but is not a provider token
// guarantee. Metadata not sent by the OpenAI adapter is excluded.
type RequestTokenEstimator struct{}

const RequestEstimatorVersion = "openai-text-json-bytes-v1"

func NewRequestTokenEstimator() *RequestTokenEstimator { return &RequestTokenEstimator{} }

func (*RequestTokenEstimator) CountText(_ string, text string) (int, error) { return len(text), nil }

// Wire fields follow eino-ext/acl/openai's text conversion. Tool type is always
// function in that adapter; schema.Message.Extra and tool Extra are not sent.
type requestMessage struct {
	Role             schema.RoleType   `json:"role"`
	Content          string            `json:"content"`
	Name             string            `json:"name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ToolCalls        []requestToolCall `json:"tool_calls,omitempty"`
}
type requestToolCall struct {
	Index    *int                `json:"index,omitempty"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function schema.FunctionCall `json:"function"`
}
type requestTool struct {
	Type     string          `json:"type"`
	Function requestFunction `json:"function"`
}
type requestFunction struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Parameters  *jsonschema.Schema `json:"parameters"`
}

func (*RequestTokenEstimator) CountMessages(_ string, messages []*schema.Message, tools []*schema.ToolInfo) (count int, err error) {
	defer func() {
		if recover() != nil {
			count, err = 0, errors.New("request representation cannot be counted")
		}
	}()
	if len(messages) == 0 {
		return 0, errors.New("request messages are required")
	}
	payload := struct {
		Messages []requestMessage `json:"messages"`
		Tools    []requestTool    `json:"tools,omitempty"`
	}{Messages: make([]requestMessage, 0, len(messages))}
	for _, m := range messages {
		if m == nil {
			return 0, errors.New("request message is nil")
		}
		if len(m.MultiContent) != 0 || len(m.UserInputMultiContent) != 0 || len(m.AssistantGenMultiContent) != 0 {
			return 0, errors.New("request counting only supports text messages")
		}
		switch m.Role {
		case schema.System, schema.User, schema.Assistant, schema.Tool:
		default:
			return 0, errors.New("request message role is unsupported")
		}
		wire := requestMessage{Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID, ReasoningContent: m.ReasoningContent}
		for _, call := range m.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, requestToolCall{Index: call.Index, ID: call.ID, Type: "function", Function: call.Function})
		}
		payload.Messages = append(payload.Messages, wire)
	}
	for _, info := range tools {
		if info == nil {
			return 0, errors.New("request tool definition is nil")
		}
		params, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return 0, errors.New("request tool schema conversion failed")
		}
		payload.Tools = append(payload.Tools, requestTool{Type: "function", Function: requestFunction{Name: info.Name, Description: info.Desc, Parameters: params}})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, errors.New("request representation cannot be encoded")
	}
	// JSON bytes include message separators, roles, names and tool structure.
	return len(encoded), nil
}

// SnapshotToolInfos freezes the parameter schema so binding and counting use
// the same definition even if Tool.Info returns mutable or dynamic metadata.
func SnapshotToolInfos(infos []*schema.ToolInfo) (result []*schema.ToolInfo, err error) {
	defer func() {
		if recover() != nil {
			result, err = nil, errors.New("tool schema cannot be copied")
		}
	}()
	result = make([]*schema.ToolInfo, 0, len(infos))
	for _, info := range infos {
		if info == nil {
			return nil, errors.New("tool definition is nil")
		}
		params, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return nil, errors.New("tool schema conversion failed")
		}
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, errors.New("tool schema cannot be encoded")
		}
		var frozen *jsonschema.Schema
		if err := json.Unmarshal(encoded, &frozen); err != nil {
			return nil, errors.New("tool schema cannot be copied")
		}
		if err := restoreSchemaValues(params, frozen); err != nil {
			return nil, err
		}
		copiedJSON, err := json.Marshal(frozen)
		if err != nil || !bytes.Equal(encoded, copiedJSON) {
			return nil, errors.New("tool schema copy changed its representation")
		}
		result = append(result, &schema.ToolInfo{Name: info.Name, Desc: info.Desc, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(frozen)})
	}
	return result, nil
}

// The pinned schema decoder ignores Extras and decodes arbitrary JSON numbers
// as float64. Restore those values with UseNumber at every nested schema.
func restoreSchemaValues(source, copy *jsonschema.Schema) error {
	if source == nil || copy == nil {
		if source != copy {
			return errors.New("tool schema copy changed its structure")
		}
		return nil
	}
	values := struct {
		Enum     []any
		Const    any
		Default  any
		Examples []any
		Extras   map[string]any
	}{source.Enum, source.Const, source.Default, source.Examples, source.Extras}
	encoded, err := json.Marshal(values)
	if err != nil {
		return errors.New("tool schema values cannot be encoded")
	}
	// Decode into empty containers; decoding over source-backed maps/slices
	// would reuse their storage and break snapshot isolation.
	values.Enum, values.Const, values.Default, values.Examples, values.Extras = nil, nil, nil, nil, nil
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil {
		return errors.New("tool schema values cannot be copied")
	}
	copy.Enum, copy.Const, copy.Default, copy.Examples, copy.Extras = values.Enum, values.Const, values.Default, values.Examples, values.Extras
	pairs := [][2]*jsonschema.Schema{{source.Not, copy.Not}, {source.If, copy.If}, {source.Then, copy.Then}, {source.Else, copy.Else}, {source.Items, copy.Items}, {source.Contains, copy.Contains}, {source.AdditionalProperties, copy.AdditionalProperties}, {source.PropertyNames, copy.PropertyNames}, {source.ContentSchema, copy.ContentSchema}}
	for _, pair := range pairs {
		if err := restoreSchemaValues(pair[0], pair[1]); err != nil {
			return err
		}
	}
	arrays := [][2][]*jsonschema.Schema{{source.AllOf, copy.AllOf}, {source.AnyOf, copy.AnyOf}, {source.OneOf, copy.OneOf}, {source.PrefixItems, copy.PrefixItems}}
	for _, pair := range arrays {
		if len(pair[0]) != len(pair[1]) {
			return errors.New("tool schema copy changed its structure")
		}
		for i, child := range pair[0] {
			if err := restoreSchemaValues(child, pair[1][i]); err != nil {
				return err
			}
		}
	}
	maps := [][2]map[string]*jsonschema.Schema{{source.Definitions, copy.Definitions}, {source.DependentSchemas, copy.DependentSchemas}, {source.PatternProperties, copy.PatternProperties}}
	for _, pair := range maps {
		for key, child := range pair[0] {
			if err := restoreSchemaValues(child, pair[1][key]); err != nil {
				return err
			}
		}
	}
	if source.Properties != nil && copy.Properties != nil {
		for p := source.Properties.Oldest(); p != nil; p = p.Next() {
			child, _ := copy.Properties.Get(p.Key)
			if err := restoreSchemaValues(p.Value, child); err != nil {
				return err
			}
		}
	}
	return nil
}

var _ TokenEstimator = (*RequestTokenEstimator)(nil)
