package eino

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestExecutorStreamForwardsTextDeltasAndBuildsTrustedInput(t *testing.T) {
	runtime := &runtimeStub{
		reader: schema.StreamReaderFromArray([]*schema.Message{
			schema.AssistantMessage("你", nil),
			nil,
			schema.AssistantMessage("好", nil),
		}),
	}
	builder := &builderStub{runtime: runtime}
	executor, err := NewExecutor(builder)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	req := testRequest([]conversation.Message{
		{Role: conversation.MessageRoleUser, Content: "上一轮问题"},
		{Role: conversation.MessageRoleAssistant, Content: "上一轮回答"},
	})
	sink := &sinkStub{}

	if err := executor.Stream(context.Background(), req, sink); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	if got, want := sink.chunks, []string{"你", "好"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sink chunks = %#v, want %#v", got, want)
	}
	if got, want := builder.input, (chat.BuildInput{
		Conversation: req.Conversation,
		History:      req.History,
		UserMessage:  req.UserMessage,
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Build input = %#v, want %#v", got, want)
	}
	if got, want := runtime.input.Query, req.UserMessage.Content; got != want {
		t.Fatalf("runtime query = %q, want %q", got, want)
	}
	if got, want := runtime.input.InterviewContext, ""; got != want {
		t.Fatalf("runtime interview context = %q, want %q", got, want)
	}
	if got, want := len(runtime.input.History), 2; got != want {
		t.Fatalf("runtime history length = %d, want %d", got, want)
	}
	if got, want := runtime.input.History[0].Role, schema.User; got != want {
		t.Fatalf("first history role = %q, want %q", got, want)
	}
	if got, want := runtime.input.History[1].Role, schema.Assistant; got != want {
		t.Fatalf("second history role = %q, want %q", got, want)
	}
}

func TestExecutorStreamReturnsContextCancellationWhenRuntimeClosesReader(t *testing.T) {
	started := make(chan struct{})
	runtime := &runtimeStub{
		stream: func(ctx context.Context, _ chat.RuntimeInput, _ ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
			reader, writer := schema.Pipe[*schema.Message](1)
			close(started)
			go func() {
				<-ctx.Done()
				writer.Send(nil, ctx.Err())
				writer.Close()
			}()
			return reader, nil
		},
	}
	executor, err := NewExecutor(&builderStub{runtime: runtime})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executor.Stream(ctx, testRequest(nil), &sinkStub{}) }()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Stream() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stream() did not return after context cancellation")
	}
}

func TestExecutorStreamReturnsRuntimeAndSinkErrors(t *testing.T) {
	runtimeErr := errors.New("model unavailable")
	reader, writer := schema.Pipe[*schema.Message](1)
	writer.Send(nil, runtimeErr)
	writer.Close()

	executor, err := NewExecutor(&builderStub{runtime: &runtimeStub{reader: reader}})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := executor.Stream(context.Background(), testRequest(nil), &sinkStub{}); !errors.Is(err, runtimeErr) {
		t.Fatalf("Stream() error = %v, want wrapped %v", err, runtimeErr)
	}

	sinkErr := errors.New("database unavailable")
	executor, err = NewExecutor(&builderStub{runtime: &runtimeStub{
		reader: schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("hello", nil)}),
	}})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := executor.Stream(context.Background(), testRequest(nil), &sinkStub{err: sinkErr}); !errors.Is(err, sinkErr) {
		t.Fatalf("Stream() error = %v, want wrapped %v", err, sinkErr)
	}
}

func TestExecutorStreamRejectsUnsupportedInput(t *testing.T) {
	executor, err := NewExecutor(&builderStub{runtime: &runtimeStub{
		reader: schema.StreamReaderFromArray([]*schema.Message{{ToolCalls: []schema.ToolCall{{}}}}),
	}})
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := executor.Stream(context.Background(), testRequest(nil), &sinkStub{}); err == nil {
		t.Fatal("Stream() error = nil, want tool call rejection")
	}
	if err := executor.Stream(context.Background(), testRequest(nil), nil); err == nil {
		t.Fatal("Stream() error = nil, want nil sink validation error")
	}

	if _, err := toSchemaHistory([]conversation.Message{{Role: "tool", Content: "x"}}); err == nil {
		t.Fatal("toSchemaHistory() error = nil, want unsupported role error")
	}
}

type builderStub struct {
	runtime chat.Runtime
	err     error
	input   chat.BuildInput
}

func (b *builderStub) Build(_ context.Context, input chat.BuildInput) (chat.Runtime, error) {
	b.input = input
	return b.runtime, b.err
}

type runtimeStub struct {
	reader *schema.StreamReader[*schema.Message]
	err    error
	input  chat.RuntimeInput
	stream func(context.Context, chat.RuntimeInput, ...compose.Option) (*schema.StreamReader[*schema.Message], error)
}

func (r *runtimeStub) Stream(ctx context.Context, input chat.RuntimeInput, options ...compose.Option) (*schema.StreamReader[*schema.Message], error) {
	r.input = input
	if r.stream != nil {
		return r.stream(ctx, input, options...)
	}
	return r.reader, r.err
}

type sinkStub struct {
	chunks []string
	err    error
}

func (s *sinkStub) WriteChunk(_ context.Context, chunk string) error {
	if s.err != nil {
		return s.err
	}
	s.chunks = append(s.chunks, chunk)
	return nil
}

func testRequest(history []conversation.Message) chat.Request {
	return chat.Request{
		Conversation: conversation.Conversation{ID: "conversation-1"},
		History:      history,
		UserMessage: conversation.Message{
			ID:      "user-1",
			Role:    conversation.MessageRoleUser,
			Content: "这轮问题",
		},
	}
}

var _ chat.RuntimeBuilder = (*builderStub)(nil)
var _ chat.Runtime = (*runtimeStub)(nil)
var _ chat.TextSink = (*sinkStub)(nil)
