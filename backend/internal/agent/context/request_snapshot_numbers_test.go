package agentcontext

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

func TestSnapshotToolInfosPreservesArbitraryJSONNumbers(t *testing.T) {
	const large = int64(9007199254740993)
	makeValues := func() *jsonschema.Schema {
		return &jsonschema.Schema{
			Enum:     []any{large, map[string]any{"nested": large}},
			Const:    map[string]any{"value": large},
			Default:  []any{large},
			Examples: []any{map[string]any{"value": large}},
			Extras:   map[string]any{"x-large": map[string]any{"value": large}},
		}
	}
	root := makeValues()
	root.Items = makeValues()
	root.Definitions = jsonschema.Definitions{"entry": makeValues()}
	root.AnyOf = []*jsonschema.Schema{makeValues()}
	before, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := SnapshotToolInfos([]*schema.ToolInfo{{Name: "numbers", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(root)}})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := frozen[0].ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(copied)
	if err != nil || string(after) != string(before) {
		t.Fatalf("schema changed: got %s want %s, err=%v", after, before, err)
	}
	root.Enum[1].(map[string]any)["nested"] = 0
	root.Extras["x-large"].(map[string]any)["value"] = 0
	root.Items.Default.([]any)[0] = 0
	unchanged, err := json.Marshal(copied)
	if err != nil || string(unchanged) != string(before) {
		t.Fatalf("snapshot shares JSON values: %s, %v", unchanged, err)
	}
	if _, ok := copied.Enum[0].(json.Number); !ok {
		t.Fatalf("number decoded as %T", copied.Enum[0])
	}
}
