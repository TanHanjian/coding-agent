package eino

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	chat "interview-memory-agent/backend/internal/application/chat"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/schema"
	callbackutils "github.com/cloudwego/eino/utils/callbacks"
)

// newGraphEventCallbacks bridges Graph lifecycle callbacks to the active
// generation only. It does not store tool payloads in conversation history.
func newGraphEventCallbacks(request chat.Request, sink chat.TextSink) []callbacks.Handler {
	publish := func(ctx context.Context, event chat.GenerationEvent) {
		_ = sink.WriteEvent(ctx, event)
	}
	return []callbacks.Handler{
		callbacks.NewHandlerBuilder().
			OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
				if info != nil && (info.Name == "chat_model" || info.Component == components.ComponentOfChatModel) {
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventPhase, Phase: "thinking"})
				}
				return ctx
			}).
			Build(),
		callbackutils.NewHandlerHelper().
			ToolsNode(&callbackutils.ToolsNodeCallbackHandlers{
				OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *schema.Message) context.Context {
					publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventPhase, Phase: "calling-tool"})
					if input == nil {
						return ctx
					}
					for _, call := range input.ToolCalls {
						publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolInput, ToolCallID: call.ID, ToolName: call.Function.Name, Input: decodeJSON(call.Function.Arguments)})
					}
					return ctx
				},
				OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output []*schema.Message) context.Context {
					publishToolOutputs(ctx, publish, output)
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
							return ctx
						}
						if err != nil {
							return ctx
						}
						publishToolOutputs(ctx, publish, results)
					}
				},
			}).
			Handler(),
	}
}

func publishToolOutputs(ctx context.Context, publish func(context.Context, chat.GenerationEvent), results []*schema.Message) {
	for _, result := range results {
		if result == nil {
			continue
		}
		publish(ctx, chat.GenerationEvent{Kind: chat.GenerationEventToolOutput, ToolCallID: result.ToolCallID, ToolName: result.ToolName, Output: decodeJSON(result.Content)})
	}
}

func decodeJSON(raw string) any {
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		return value
	}
	return raw
}
