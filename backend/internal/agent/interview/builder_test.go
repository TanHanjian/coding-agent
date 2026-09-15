package interview

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	chat "interview-memory-agent/backend/internal/application/chat"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestNewBuilderRequiresModel(t *testing.T) {
	builder, err := NewBuilder(nil)
	if err == nil {
		t.Fatalf("expected validation error, got builder=%v", builder)
	}
}

func TestBuilderBindsToolsWithoutReplacingSourceModel(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	boundModel := &scriptedToolCallingModel{plainTextOnly: true}
	sourceModel := &toolBindingModel{
		ToolCallingChatModel: &scriptedToolCallingModel{},
		boundModel:           boundModel,
	}
	builder, err := NewBuilder(sourceModel, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}

	for range 2 {
		if _, err := builder.Build(context.Background(), chat.BuildInput{}); err != nil {
			t.Fatalf("Build() error = %v", err)
		}
	}

	if builder.chatModel != sourceModel {
		t.Fatal("Build() replaced the Builder source model")
	}
	if sourceModel.bindCalls != 2 {
		t.Fatalf("WithTools() calls = %d, want 2", sourceModel.bindCalls)
	}
	if sourceModel.lastToolCount != 1 {
		t.Fatalf("bound tool count = %d, want 1", sourceModel.lastToolCount)
	}
}

func TestNewAgentStateCreatesIndependentEmptyState(t *testing.T) {
	first := newAgentState()
	second := newAgentState()

	if first == nil || second == nil {
		t.Fatal("newAgentState() returned nil")
	}
	if first == second {
		t.Fatal("newAgentState() reused state across runs")
	}
	if first.Messages == nil || second.Messages == nil {
		t.Fatal("newAgentState() returned a nil message slice")
	}
	if len(first.Messages) != 0 || len(second.Messages) != 0 {
		t.Fatalf("newAgentState() messages = %v, %v; want empty slices", first.Messages, second.Messages)
	}
	if first.ToolRounds != 0 || second.ToolRounds != 0 {
		t.Fatalf("newAgentState() tool rounds = %d, %d; want 0", first.ToolRounds, second.ToolRounds)
	}

	first.Messages = append(first.Messages, nil)
	if len(second.Messages) != 0 {
		t.Fatal("agent states share message storage")
	}
}

func TestInitializeMessageStateInitializesOnce(t *testing.T) {
	state := newAgentState()
	messages := []*schema.Message{schema.SystemMessage("system"), schema.UserMessage("question")}

	output, err := initializeMessageState(context.Background(), messages, state)
	if err != nil {
		t.Fatalf("initializeMessageState() error = %v", err)
	}
	if len(output) != len(messages) || len(state.Messages) != len(messages) {
		t.Fatalf("message count = output:%d state:%d; want %d", len(output), len(state.Messages), len(messages))
	}
	if _, err := initializeMessageState(context.Background(), messages, state); err == nil {
		t.Fatal("initializeMessageState() error = nil; want duplicate initialization error")
	}
}

func TestModelStateInputReturnsAccumulatedMessages(t *testing.T) {
	state := newAgentState()
	if _, err := modelStateInput(context.Background(), nil, state); err == nil {
		t.Fatal("modelStateInput() error = nil; want uninitialized state error")
	}

	state.Messages = append(state.Messages, schema.UserMessage("question"))
	output, err := modelStateInput(context.Background(), []*schema.Message{schema.ToolMessage("ignored", "call-1")}, state)
	if err != nil {
		t.Fatalf("modelStateInput() error = %v", err)
	}
	if len(output) != 1 || output[0].Content != "question" {
		t.Fatalf("modelStateInput() = %#v; want accumulated state messages", output)
	}
	if len(state.Messages) != 1 {
		t.Fatalf("modelStateInput() mutated state to %d messages; want 1", len(state.Messages))
	}
}

func TestToolStateHandlersRecordCallAndResults(t *testing.T) {
	state := newAgentState()
	toolCall := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID: "call-1",
			Function: schema.FunctionCall{
				Name:      "search_questions",
				Arguments: `{"query":"Go"}`,
			},
		}},
	}

	if _, err := recordToolCall(context.Background(), toolCall, state); err != nil {
		t.Fatalf("recordToolCall() error = %v", err)
	}
	if state.ToolRounds != 1 || len(state.Messages) != 1 {
		t.Fatalf("state after call = rounds:%d messages:%d; want rounds:1 messages:1", state.ToolRounds, len(state.Messages))
	}

	results := []*schema.Message{schema.ToolMessage("result", "call-1")}
	output, err := recordToolResults(context.Background(), results, state)
	if err != nil {
		t.Fatalf("recordToolResults() error = %v", err)
	}
	if len(output) != 1 || len(state.Messages) != 2 {
		t.Fatalf("state after results = output:%d messages:%d; want output:1 messages:2", len(output), len(state.Messages))
	}
}

func TestRecordToolResultsRejectsMismatchedToolCallID(t *testing.T) {
	state := newAgentState()
	toolCall := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{newSearchToolCall("call-1", "Go")}}
	if _, err := recordToolCall(context.Background(), toolCall, state); err != nil {
		t.Fatalf("recordToolCall() error = %v", err)
	}

	_, err := recordToolResults(
		context.Background(),
		[]*schema.Message{schema.ToolMessage("result", "unknown-call")},
		state,
	)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("recordToolResults() error = %v, want mismatched tool call id", err)
	}
	if len(state.Messages) != 1 {
		t.Fatalf("recordToolResults() recorded rejected result; messages = %d", len(state.Messages))
	}
}

func TestRecordToolCallRejectsInvalidOrExhaustedState(t *testing.T) {
	state := newAgentState()
	if _, err := recordToolCall(context.Background(), nil, state); err == nil {
		t.Fatal("recordToolCall(nil) error = nil; want validation error")
	}

	state.ToolRounds = maxToolIterations
	message := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{}}}
	if _, err := recordToolCall(context.Background(), message, state); err == nil {
		t.Fatal("recordToolCall() error = nil; want iteration limit error")
	}
	if len(state.Messages) != 0 {
		t.Fatalf("recordToolCall() recorded rejected call; messages = %d", len(state.Messages))
	}
}

func TestBuilderGraphRunsPlainTextToEnd(t *testing.T) {
	chatModel := &scriptedToolCallingModel{plainTextOnly: true}
	builder, err := NewBuilder(chatModel)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	reader, err := runtime.Stream(context.Background(), GraphInput{Query: "普通回答"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer reader.Close()

	var output string
	for {
		message, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("runtime stream error = %v", recvErr)
		}
		if message != nil {
			output += message.Content
		}
	}
	if output != "普通答案" {
		t.Fatalf("graph output = %q, want plain answer", output)
	}
	if len(chatModel.inputs) != 1 {
		t.Fatalf("model invocation count = %d, want 1", len(chatModel.inputs))
	}
}

func TestBuilderGraphCallbackUsesConfiguredNodeNames(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	builder, err := NewBuilder(&scriptedToolCallingModel{}, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	var mutex sync.Mutex
	names := make(map[string]bool)
	handler := callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			if info != nil {
				mutex.Lock()
				names[info.Name] = true
				mutex.Unlock()
			}
			return ctx
		}).
		Build()

	reader, err := runtime.Stream(
		context.Background(),
		GraphInput{Query: "普通回答"},
		compose.WithCallbacks(handler),
	)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer reader.Close()
	for {
		_, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("runtime stream error = %v", recvErr)
		}
	}

	for _, want := range []string{"prepare_context", "chat_template", "chat_model", "tools"} {
		if !names[want] {
			t.Fatalf("callback names = %#v, missing %q", names, want)
		}
	}
}

func TestBuilderGraphRunsToolLoopAndHidesIntermediateMessages(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	chatModel := &scriptedToolCallingModel{}
	builder, err := NewBuilder(chatModel, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	reader, err := runtime.Stream(context.Background(), GraphInput{Query: "二叉树"})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer reader.Close()

	var output string
	for {
		message, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			t.Fatalf("runtime stream error = %v", recvErr)
		}
		if message != nil {
			output += message.Content
		}
	}

	if output != "最终答案" {
		t.Fatalf("graph output = %q, want final answer only", output)
	}
	if len(chatModel.inputs) != 2 {
		t.Fatalf("model invocation count = %d, want 2", len(chatModel.inputs))
	}
	if got := len(chatModel.inputs[1]); got != 5 {
		t.Fatalf("second model input length = %d, want initial 3 + call + result", got)
	}
	if chatModel.inputs[1][3].Role != schema.Assistant || len(chatModel.inputs[1][3].ToolCalls) != 1 {
		t.Fatal("second model input is missing the assistant tool call")
	}
	if chatModel.inputs[1][4].Role != schema.Tool {
		t.Fatal("second model input is missing the tool result")
	}
}

func TestBuilderGraphStreamsTextWithoutWaitingForLateToolCall(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	chatModel := &scriptedToolCallingModel{contentBeforeToolCall: true}
	builder, err := NewBuilder(chatModel, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	output, err := readRuntimeStream(runtime, GraphInput{Query: "延迟工具调用"})
	if err != nil {
		t.Fatalf("runtime stream error = %v", err)
	}
	if output != "最终答案" {
		t.Fatalf("graph output = %q, want final graph output", output)
	}
	if len(chatModel.inputs) != 2 {
		t.Fatalf("model invocation count = %d, want 2", len(chatModel.inputs))
	}
}

func TestBuilderGraphSupportsMultipleToolCalls(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	chatModel := &scriptedToolCallingModel{initialToolCalls: []schema.ToolCall{
		newSearchToolCall("call-1", "Go"),
		newSearchToolCall("call-2", "Rust"),
	}}
	builder, err := NewBuilder(chatModel, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	output, err := readRuntimeStream(runtime, GraphInput{Query: "比较语言"})
	if err != nil {
		t.Fatalf("runtime stream error = %v", err)
	}
	if output != "最终答案" {
		t.Fatalf("graph output = %q, want final answer", output)
	}
	if got := len(chatModel.inputs[1]); got != 6 {
		t.Fatalf("second model input length = %d, want initial 3 + call + 2 results", got)
	}
	if chatModel.inputs[1][4].ToolCallID != "call-1" || chatModel.inputs[1][5].ToolCallID != "call-2" {
		t.Fatalf("tool result ids = %q, %q; want call-1, call-2", chatModel.inputs[1][4].ToolCallID, chatModel.inputs[1][5].ToolCallID)
	}
}

func TestBuilderGraphReturnsToolExecutionError(t *testing.T) {
	failingTool := newFailingTool(t)
	chatModel := &scriptedToolCallingModel{initialToolCalls: []schema.ToolCall{{
		ID: "call-1",
		Function: schema.FunctionCall{
			Name:      "failing_tool",
			Arguments: `{"query":"Go"}`,
		},
	}}}
	builder, err := NewBuilder(chatModel, failingTool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	_, err = readRuntimeStream(runtime, GraphInput{Query: "触发工具失败"})
	if err == nil || !strings.Contains(err.Error(), "fixture tool failure") {
		t.Fatalf("runtime error = %v, want tool execution failure", err)
	}
}

func TestRouteModelOutputReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := schema.Pipe[*schema.Message](1)
	cancel()
	writer.Send(nil, context.Canceled)
	writer.Close()

	_, err := routeModelOutput(ctx, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("routeModelOutput() error = %v, want context.Canceled", err)
	}
}

func TestBuilderGraphRejectsSeventhToolRound(t *testing.T) {
	tool, err := NewFakeSearchQuestionsTool()
	if err != nil {
		t.Fatalf("NewFakeSearchQuestionsTool() error = %v", err)
	}
	builder, err := NewBuilder(&scriptedToolCallingModel{alwaysToolCall: true}, tool)
	if err != nil {
		t.Fatalf("NewBuilder() error = %v", err)
	}
	runtime, err := builder.Build(context.Background(), chat.BuildInput{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	_, err = runtime.Stream(context.Background(), GraphInput{Query: "loop"})
	if err == nil {
		t.Fatal("Stream() error = nil; want tool iteration limit error")
	}
	if !strings.Contains(err.Error(), "maximum tool iterations exceeded") {
		t.Fatalf("Stream() error = %v, want tool iteration limit", err)
	}
}

type scriptedToolCallingModel struct {
	inputs                [][]*schema.Message
	alwaysToolCall        bool
	plainTextOnly         bool
	contentBeforeToolCall bool
	initialToolCalls      []schema.ToolCall
}

type toolBindingModel struct {
	model.ToolCallingChatModel
	boundModel    model.ToolCallingChatModel
	bindCalls     int
	lastToolCount int
}

func (m *toolBindingModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.bindCalls++
	m.lastToolCount = len(tools)
	return m.boundModel, nil
}

func (m *scriptedToolCallingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *scriptedToolCallingModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	stream, err := m.Stream(ctx, input)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var result *schema.Message
	for {
		message, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return result, nil
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if message != nil {
			result = message
		}
	}
}

func (m *scriptedToolCallingModel) Stream(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.inputs = append(m.inputs, append([]*schema.Message(nil), input...))
	if m.plainTextOnly {
		return schema.StreamReaderFromArray([]*schema.Message{
			schema.AssistantMessage("普通", nil),
			schema.AssistantMessage("答案", nil),
		}), nil
	}
	if m.alwaysToolCall || len(m.inputs) == 1 {
		toolCalls := m.initialToolCalls
		if len(toolCalls) == 0 {
			toolCalls = []schema.ToolCall{newSearchToolCall("call-1", "二叉树")}
		}
		chunks := make([]*schema.Message, 0, 2)
		if m.contentBeforeToolCall {
			chunks = append(chunks, schema.AssistantMessage("我先查一下。", nil))
		}
		chunks = append(chunks, &schema.Message{Role: schema.Assistant, ToolCalls: toolCalls})
		return schema.StreamReaderFromArray(chunks), nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{
		schema.AssistantMessage("最终", nil),
		schema.AssistantMessage("答案", nil),
	}), nil
}

func newSearchToolCall(id, query string) schema.ToolCall {
	return schema.ToolCall{
		ID: id,
		Function: schema.FunctionCall{
			Name:      "search_questions",
			Arguments: `{"query":"` + query + `"}`,
		},
	}
}

type failingToolInput struct {
	Query string `json:"query" jsonschema_description:"Question search text"`
}

func newFailingTool(t *testing.T) tool.InvokableTool {
	t.Helper()
	result, err := toolutils.InferTool[failingToolInput, string](
		"failing_tool",
		"Always returns a deterministic test error.",
		func(_ context.Context, _ failingToolInput) (string, error) {
			return "", errors.New("fixture tool failure")
		},
	)
	if err != nil {
		t.Fatalf("InferTool() error = %v", err)
	}
	return result
}

func readRuntimeStream(runtime chat.Runtime, input GraphInput) (string, error) {
	reader, err := runtime.Stream(context.Background(), input)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var output string
	for {
		message, recvErr := reader.Recv()
		if errors.Is(recvErr, io.EOF) {
			return output, nil
		}
		if recvErr != nil {
			return "", recvErr
		}
		if message != nil {
			output += message.Content
		}
	}
}
