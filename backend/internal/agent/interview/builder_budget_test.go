package interview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	agenteino "interview-memory-agent/backend/internal/agent/eino"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

func testBudgetGuard(t *testing.T) *agentcontext.ModelCallGuard {
	t.Helper()
	return newTestBudgetGuard(t, 1_000_000, agentcontext.NewRequestTokenEstimator())
}

func newTestBudgetGuard(t *testing.T, available int, estimator agentcontext.TokenEstimator) *agentcontext.ModelCallGuard {
	t.Helper()
	guard, err := agentcontext.NewModelCallGuard(agentcontext.ModelCallConfig{
		ModelID: "graph-test-model", ContextWindowTokens: available + 128 + 32,
		OutputLimitTokens: 128, SafetyMarginTokens: 32,
	}, estimator)
	if err != nil {
		t.Fatalf("NewModelCallGuard() error = %v", err)
	}
	return guard
}

func TestNewBuilderRequiresBudgetGuard(t *testing.T) {
	for _, withProvider := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_provider", true: "explicit_provider"}[withProvider], func(t *testing.T) {
			var builder *Builder
			var err error
			if withProvider {
				builder, err = NewBuilderWithPromptProvider(&scriptedToolCallingModel{}, agentprompt.NewLocalPromptProvider(), nil)
			} else {
				builder, err = NewBuilder(&scriptedToolCallingModel{}, nil)
			}
			if builder != nil || err == nil || !strings.Contains(err.Error(), "model budget guard is required") {
				t.Fatalf("constructor = %v, %v; want nil builder and required guard error", builder, err)
			}
		})
	}
}

func TestBuilderGraphBudgetRejectsFirstModelCall(t *testing.T) {
	for _, countingFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget_exceeded", true: "counting_failed"}[countingFailure], func(t *testing.T) {
			estimator := &graphBudgetEstimator{}
			want := chat.ErrContextBudgetExceeded
			if countingFailure {
				estimator.err = errors.New("fixture counting failure")
				want = chat.ErrContextCountingFailed
			}
			chatModel := &scriptedToolCallingModel{plainTextOnly: true}
			builder, err := NewBuilder(chatModel, newTestBudgetGuard(t, 1, estimator))
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := builder.Build(t.Context(), chat.BuildInput{})
			if err != nil {
				t.Fatal(err)
			}
			output, err := readRuntimeStream(runtime, GraphInput{Query: "must not reach model"})
			if !errors.Is(err, want) || err == want {
				t.Fatalf("Graph streaming error = %v; want wrapped %v", err, want)
			}
			if output != "" || len(chatModel.inputs) != 0 || len(estimator.inputs) != 1 {
				t.Fatalf("output=%q model calls=%d counts=%d; want empty, 0, 1", output, len(chatModel.inputs), len(estimator.inputs))
			}
			if len(estimator.inputs[0]) != 3 || estimator.inputs[0][2].Content != "must not reach model" {
				t.Fatalf("counted input = %+v; want completed prompt", estimator.inputs[0])
			}
		})
	}
}

func TestBuilderGraphBudgetRejectsExpandedToolResultBeforeSecondModelCall(t *testing.T) {
	const available = 10_000
	const toolName = "graph_budget_large_result"
	if _, exists := conversation.GetToolPolicy(toolName, "1"); !exists {
		if err := conversation.RegisterToolPolicy(conversation.ToolPersistencePolicy{
			ToolName: toolName, Version: "1",
			ArgumentFields:  []conversation.ProjectionFieldRule{{SourceKey: "query", StoredKey: "query", MaxBytes: 128}},
			ResultFields:    []conversation.ProjectionFieldRule{{SourceKey: "result", StoredKey: "result", MaxBytes: 128}},
			MaxPreviewBytes: 128,
		}); err != nil {
			t.Fatal(err)
		}
	}
	largeResult := `{"result":"` + strings.Repeat("large tool result ", available) + `"}`
	currentTool := newGraphBudgetTool(largeResult)
	currentTool.info.Name = toolName
	recorder := &graphBudgetRecorder{}
	estimator := &graphBudgetEstimator{beforeCount: func() { recorder.order = append(recorder.order, "count") }}
	currentTool.beforeRun = func() { recorder.order = append(recorder.order, "tool") }
	toolCall := newSearchToolCall("call-1", "二叉树")
	toolCall.Function.Name = toolName
	chatModel := &scriptedToolCallingModel{contentBeforeToolCall: true, initialToolCalls: []schema.ToolCall{toolCall}}
	builder, err := NewBuilder(chatModel, newTestBudgetGuard(t, available, estimator), currentTool)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agenteino.NewExecutor(builder)
	if err != nil {
		t.Fatal(err)
	}
	sink := &graphBudgetSink{}
	err = executor.Stream(t.Context(), chat.Request{
		UserMessage: conversation.Message{Role: conversation.MessageRoleUser, Content: "fetch large result"},
		Recorder:    recorder,
	}, sink)
	if !errors.Is(err, chat.ErrContextBudgetExceeded) || err == chat.ErrContextBudgetExceeded {
		t.Fatalf("Graph streaming error = %v; want wrapped budget error", err)
	}
	if len(chatModel.inputs) != 1 || currentTool.runCalls != 1 || len(estimator.inputs) != 2 {
		t.Fatalf("model calls=%d tool calls=%d counts=%d; want 1, 1, 2", len(chatModel.inputs), currentTool.runCalls, len(estimator.inputs))
	}
	if estimator.counts[0] > available || estimator.counts[1] <= available {
		t.Fatalf("counts=%v available=%d; want first allowed, second rejected", estimator.counts, available)
	}
	second := estimator.inputs[1]
	if len(second) != 5 || second[3].Content != "我先查一下。" || len(second[3].ToolCalls) != 1 || second[4].Role != schema.Tool || second[4].ToolCallID != "call-1" || second[4].Content != largeResult {
		t.Fatalf("second budget input does not include complete assistant call and large result: messages=%d", len(second))
	}
	if sink.text != "我先查一下。" {
		t.Fatalf("visible text = %q; want partial text before budget rejection", sink.text)
	}
	if !reflect.DeepEqual(recorder.order, []string{"count", "assistant", "tool", "results", "count"}) || len(recorder.assistants) != 1 || len(recorder.results) != 1 || len(recorder.results[0]) != 1 {
		t.Fatalf("recorder ordering/facts = %+v; want closed tool group before rejected count", recorder)
	}
	call := recorder.assistants[0]
	result := recorder.results[0][0].ToolResult
	if call.StopReason != conversation.EntryStopTools || len(call.Content) != 2 || call.Content[0].Text == nil || *call.Content[0].Text != sink.text || call.Content[1].ToolCall == nil || call.Content[1].ToolCall.ID() != "call-1" || result == nil || result.CallEntryID != "budget-call-entry" || result.CallID != "call-1" || result.ToolName != toolName || result.IsError {
		t.Fatalf("recorded assistant/result facts do not close the tool call: call=%+v result=%+v", call, result)
	}
}

func TestBuilderGraphBudgetErrorSurvivesStreamRecvWrapping(t *testing.T) {
	for _, countingFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget_exceeded", true: "counting_failed"}[countingFailure], func(t *testing.T) {
			estimator := &graphBudgetEstimator{}
			want := chat.ErrContextBudgetExceeded
			if countingFailure {
				estimator.err = errors.New("fixture counting failure")
				want = chat.ErrContextCountingFailed
			}
			chatModel := &scriptedToolCallingModel{plainTextOnly: true}
			builder, err := NewBuilder(chatModel, newTestBudgetGuard(t, 1, estimator))
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := builder.Build(t.Context(), chat.BuildInput{})
			if err != nil {
				t.Fatal(err)
			}

			// Interview's branching Graph returns pre-handler errors eagerly.
			// A streaming parent can relay that rejection through its output;
			// exercise a real Eino Graph reader and its conversion wrappers.
			parent := compose.NewGraph[GraphInput, *schema.Message]()
			if err := parent.AddLambdaNode("interview", compose.StreamableLambda(func(ctx context.Context, input GraphInput) (*schema.StreamReader[*schema.Message], error) {
				output, err := runtime.Stream(ctx, input)
				if err == nil {
					return output, nil
				}
				reader, writer := schema.Pipe[*schema.Message](1)
				writer.Send(nil, fmt.Errorf("nested interview stream: %w", err))
				writer.Close()
				return reader, nil
			})); err != nil {
				t.Fatal(err)
			}
			if err := parent.AddEdge(compose.START, "interview"); err != nil {
				t.Fatal(err)
			}
			if err := parent.AddEdge("interview", compose.END); err != nil {
				t.Fatal(err)
			}
			compiled, err := parent.Compile(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			reader, err := compiled.Stream(t.Context(), GraphInput{Query: "budget error via Recv"})
			if err != nil {
				t.Fatalf("parent Stream() error = %v; want error from Recv", err)
			}
			defer reader.Close()
			message, err := reader.Recv()
			if message != nil || err == want || !errors.Is(err, want) || !strings.Contains(err.Error(), "nested interview stream:") {
				t.Fatalf("Recv() = %+v, %v; want wrapped %v", message, err, want)
			}
			if _, err := reader.Recv(); !errors.Is(err, io.EOF) {
				t.Fatalf("second Recv() error = %v; want EOF", err)
			}
			if len(chatModel.inputs) != 0 || len(estimator.inputs) != 1 {
				t.Fatalf("model calls=%d counts=%d; want 0, 1", len(chatModel.inputs), len(estimator.inputs))
			}
		})
	}
}

func TestBuilderGraphBudgetCountsEveryRoundAndRunWithoutCaching(t *testing.T) {
	currentTool := newGraphBudgetTool(`{"result":"small"}`)
	estimator := &graphBudgetEstimator{}
	chatModel := &scriptedToolCallingModel{alwaysToolCall: true}
	builder, err := NewBuilder(chatModel, newTestBudgetGuard(t, 1_000_000, estimator), currentTool)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := builder.Build(t.Context(), chat.BuildInput{})
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run < 2; run++ {
		_, err := readRuntimeStream(runtime, GraphInput{Query: "same query"})
		if err == nil || !strings.Contains(err.Error(), "maximum tool iterations exceeded") {
			t.Fatalf("run %d error = %v; want tool iteration limit", run, err)
		}
	}
	const callsPerRun = maxToolIterations + 1
	if len(estimator.inputs) != 2*callsPerRun || len(chatModel.inputs) != 2*callsPerRun || currentTool.runCalls != 2*maxToolIterations {
		t.Fatalf("counts=%d model calls=%d tool calls=%d; want %d, %d, %d", len(estimator.inputs), len(chatModel.inputs), currentTool.runCalls, 2*callsPerRun, 2*callsPerRun, 2*maxToolIterations)
	}
	for i, input := range estimator.inputs {
		if len(input) != 3+2*(i%callsPerRun) || !reflect.DeepEqual(input, chatModel.inputs[i]) {
			t.Fatalf("call %d counted input differs from full model input: length=%d", i+1, len(input))
		}
	}
	if estimator.counts[0] != estimator.counts[callsPerRun] {
		t.Fatalf("run state leaked: first request counts=%d, %d", estimator.counts[0], estimator.counts[callsPerRun])
	}
}

func TestBuilderGraphBudgetUsesFrozenDynamicToolSchema(t *testing.T) {
	currentTool := newGraphBudgetTool(`{"result":"small"}`)
	estimator := &graphBudgetEstimator{}
	boundModel := &scriptedToolCallingModel{}
	sourceModel := &graphBudgetBindingModel{scriptedToolCallingModel: &scriptedToolCallingModel{}, bound: boundModel}
	builder, err := NewBuilder(sourceModel, newTestBudgetGuard(t, 1_000_000, estimator), currentTool)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := builder.Build(t.Context(), chat.BuildInput{})
	if err != nil {
		t.Fatal(err)
	}
	if currentTool.infoCalls != 1 || sourceModel.bindCalls != 1 || len(sourceModel.infos) != 1 {
		t.Fatalf("Info calls=%d bind calls=%d bound infos=%d; want 1 each", currentTool.infoCalls, sourceModel.bindCalls, len(sourceModel.infos))
	}
	frozen := sourceModel.infos[0]
	if frozen == currentTool.info {
		t.Fatal("WithTools received mutable source ToolInfo")
	}
	currentTool.info.Name = "changed_tool"
	currentTool.info.Desc = "changed description"
	currentTool.params.Description = "changed parameter schema"
	output, err := readRuntimeStream(runtime, GraphInput{Query: "frozen schema"})
	if err != nil || output != "最终答案" {
		t.Fatalf("Graph streaming output=%q error=%v; want final answer", output, err)
	}
	if currentTool.infoCalls != 1 || currentTool.runCalls != 1 || len(boundModel.inputs) != 2 || len(sourceModel.inputs) != 0 || len(estimator.tools) != 2 {
		t.Fatalf("Info calls=%d tools=%d bound model calls=%d source model calls=%d counts=%d", currentTool.infoCalls, currentTool.runCalls, len(boundModel.inputs), len(sourceModel.inputs), len(estimator.tools))
	}
	for i, infos := range estimator.tools {
		if len(infos) != 1 || infos[0] != frozen {
			t.Fatalf("count %d did not use WithTools frozen ToolInfo", i+1)
		}
	}
	params, err := frozen.ParamsOneOf.ToJSONSchema()
	if err != nil || frozen.Name != "search_questions" || frozen.Desc != "initial tool description" || params.Description != "initial parameter schema" || params == currentTool.params {
		t.Fatalf("frozen schema changed: info=%+v params=%+v error=%v", frozen, params, err)
	}
	if currentTool.lastArgs != `{"query":"二叉树"}` {
		t.Fatalf("ToolsNode used wrong definition/arguments: %q", currentTool.lastArgs)
	}
}

// Each invocation captures the completed request and delegates to production
// counting. Copy the slice because Graph state appends messages in later rounds.
type graphBudgetEstimator struct {
	inputs      [][]*schema.Message
	tools       [][]*schema.ToolInfo
	counts      []int
	err         error
	beforeCount func()
}

func (*graphBudgetEstimator) CountText(_ string, text string) (int, error) { return len(text), nil }

func (e *graphBudgetEstimator) CountMessages(model string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error) {
	e.inputs = append(e.inputs, append([]*schema.Message(nil), messages...))
	e.tools = append(e.tools, append([]*schema.ToolInfo(nil), tools...))
	if e.beforeCount != nil {
		e.beforeCount()
	}
	if e.err != nil {
		return 0, e.err
	}
	count, err := agentcontext.NewRequestTokenEstimator().CountMessages(model, messages, tools)
	e.counts = append(e.counts, count)
	return count, err
}

type graphBudgetTool struct {
	info      *schema.ToolInfo
	params    *jsonschema.Schema
	infoCalls int
	runCalls  int
	lastArgs  string
	result    string
	beforeRun func()
}

func newGraphBudgetTool(result string) *graphBudgetTool {
	params := &jsonschema.Schema{Type: "object", Description: "initial parameter schema"}
	return &graphBudgetTool{
		info:   &schema.ToolInfo{Name: "search_questions", Desc: "initial tool description", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(params)},
		params: params, result: result,
	}
}

func (t *graphBudgetTool) Info(context.Context) (*schema.ToolInfo, error) {
	t.infoCalls++
	if t.infoCalls > 1 {
		return &schema.ToolInfo{Name: "changed_tool", ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{Type: "string"})}, nil
	}
	return t.info, nil
}

func (t *graphBudgetTool) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	t.runCalls++
	if t.beforeRun != nil {
		t.beforeRun()
	}
	t.lastArgs = args
	return t.result, nil
}

type graphBudgetBindingModel struct {
	*scriptedToolCallingModel
	bound     *scriptedToolCallingModel
	bindCalls int
	infos     []*schema.ToolInfo
}

func (m *graphBudgetBindingModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.bindCalls++
	m.infos = infos
	return m.bound, nil
}

type graphBudgetRecorder struct {
	order      []string
	assistants []conversation.EntryMessage
	results    [][]conversation.EntryMessage
}

func (r *graphBudgetRecorder) RecordAssistant(_ context.Context, message conversation.EntryMessage) (conversation.EntryID, error) {
	r.order = append(r.order, "assistant")
	r.assistants = append(r.assistants, message.Clone())
	return "budget-call-entry", nil
}

func (r *graphBudgetRecorder) RecordToolResults(_ context.Context, messages []conversation.EntryMessage) error {
	r.order = append(r.order, "results")
	cloned := make([]conversation.EntryMessage, len(messages))
	for i, message := range messages {
		cloned[i] = message.Clone()
	}
	r.results = append(r.results, cloned)
	return nil
}

type graphBudgetSink struct{ text string }

func (s *graphBudgetSink) WriteChunk(_ context.Context, chunk string) error {
	s.text += chunk
	return nil
}

func (*graphBudgetSink) WriteEvent(context.Context, chat.GenerationEvent) error { return nil }
