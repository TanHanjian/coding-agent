package eino

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"

	chat "interview-memory-agent/backend/internal/application/chat"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	callbackutils "github.com/cloudwego/eino/utils/callbacks"
)

// newGraphEventCallbacks bridges Graph lifecycle callbacks to the active
// generation only. It does not store tool payloads in conversation history.
func newGraphEventCallbacks(request chat.Request, sink chat.TextSink, steps *generationStepTracker) []callbacks.Handler {
	publish := func(ctx context.Context, event chat.GenerationEvent) {
		_ = sink.WriteEvent(ctx, event)
	}
	return []callbacks.Handler{
		callbacks.NewHandlerBuilder().
			OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
				if info != nil && (info.Name == "chat_model" || info.Component == components.ComponentOfChatModel) {
					stepID := steps.Begin()
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventStepStart, StepID: stepID})
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventPhase, StepID: stepID, Phase: "thinking"})
				}
				return ctx
			}).
			Build(),
		callbackutils.NewHandlerHelper().
			ChatModel(&callbackutils.ModelCallbackHandler{
				OnEndWithStreamOutput: func(ctx context.Context, _ *callbacks.RunInfo, output *schema.StreamReader[*model.CallbackOutput]) context.Context {
					if output == nil {
						return ctx
					}
					defer output.Close()
					for {
						chunk, err := output.Recv()
						if errors.Is(err, io.EOF) || err != nil {
							return ctx
						}
						if chunk == nil || chunk.Message == nil || chunk.Message.Content == "" {
							continue
						}
						_ = sink.WriteChunk(ctx, chunk.Message.Content)
					}
				},
			}).
			ToolsNode(&callbackutils.ToolsNodeCallbackHandlers{
				OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *schema.Message) context.Context {
					stepID := steps.Current()
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventPhase, StepID: stepID, Phase: "calling-tool"})
					if input == nil {
						return ctx
					}
					for _, call := range input.ToolCalls {
						publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolInput, StepID: stepID, ToolCallID: call.ID, ToolName: call.Function.Name, Input: toolInputSummary(call.Function.Name)})
					}
					return ctx
				},
				OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output []*schema.Message) context.Context {
					publishToolOutputs(ctx, publish, steps.Current(), output)
					finishStep(ctx, publish, steps)
					return ctx
				},
				OnEndWithStreamOutput: func(ctx context.Context, _ *callbacks.RunInfo, output *schema.StreamReader[[]*schema.Message]) context.Context {
					if output == nil {
						return ctx
					}
					defer output.Close()
					for {
						results, err := output.Recv()
						if errors.Is(err, io.EOF) {
							finishStep(ctx, publish, steps)
							return ctx
						}
						if err != nil {
							publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolOutputError, StepID: steps.Current(), ErrorText: "工具调用失败"})
							finishStep(ctx, publish, steps)
							return ctx
						}
						publishToolOutputs(ctx, publish, steps.Current(), results)
					}
				},
				OnError: func(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context {
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolOutputError, StepID: steps.Current(), ErrorText: "工具调用失败"})
					finishStep(ctx, publish, steps)
					return ctx
				},
			}).
			Handler(),
	}
}

func publishToolOutputs(ctx context.Context, publish func(context.Context, chat.GenerationEvent), stepID string, results []*schema.Message) {
	for _, result := range results {
		if result == nil {
			continue
		}
		publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolOutput, StepID: stepID, ToolCallID: result.ToolCallID, ToolName: result.ToolName, Output: toolOutputSummary(result.ToolName)})
	}
}

func finishStep(ctx context.Context, publish func(context.Context, chat.GenerationEvent), steps *generationStepTracker) {
	if stepID := steps.Finish(); stepID != "" {
		publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventStepFinish, StepID: stepID})
	}
}

// Tool summaries are deliberately independent from raw tool JSON. The UI can
// show that a tool is active or completed without exposing private arguments,
// returned source material, or implementation errors.
func toolInputSummary(toolName string) map[string]string {
	return map[string]string{"status": "已提交", "tool": toolName}
}

func toolOutputSummary(toolName string) map[string]string {
	return map[string]string{"status": "已完成", "tool": toolName}
}

type generationStepTracker struct {
	mu       sync.Mutex
	next     int
	current  string
	finished bool
}

func newGenerationStepTracker() *generationStepTracker {
	return &generationStepTracker{}
}

func (s *generationStepTracker) Begin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.current = "step-" + strconv.Itoa(s.next)
	s.finished = false
	return s.current
}

func (s *generationStepTracker) Current() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *generationStepTracker) HasStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current != ""
}

func (s *generationStepTracker) Finish() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == "" || s.finished {
		return ""
	}
	s.finished = true
	return s.current
}
