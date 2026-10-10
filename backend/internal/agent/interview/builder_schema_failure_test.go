package interview

import (
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
)

func TestBuilderRejectsMalformedToolSchemaWithoutModelCall(t *testing.T) {
	model := &scriptedToolCallingModel{}
	currentTool := newGraphBudgetTool("unused")
	currentTool.info.ParamsOneOf = schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"PRIVATE": nil})
	builder, err := NewBuilder(model, testBudgetGuard(t), currentTool)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := builder.Build(t.Context(), chat.BuildInput{})
	if runtime != nil || !errors.Is(err, chat.ErrContextCountingFailed) {
		t.Fatalf("malformed schema build = %v, %v", runtime, err)
	}
	if strings.Contains(err.Error(), "PRIVATE") || len(model.inputs) != 0 {
		t.Fatalf("unsafe error or model call: %v, calls=%d", err, len(model.inputs))
	}
}
