package eino

import (
	"context"
	"log/slog"
	"time"

	chat "interview-memory-agent/backend/internal/application/chat"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/schema"
	callbackutils "github.com/cloudwego/eino/utils/callbacks"
)

type graphDebugStartKey struct{}

type graphDebugRun struct {
	conversationID     string
	assistantMessageID string
}

// newGraphDebugCallbacks creates callbacks for exactly one Agent run. The
// callbacks intentionally log metadata only: never prompts, model text, tool
// arguments, or tool results.
func newGraphDebugCallbacks(request chat.Request) []callbacks.Handler {
	run := graphDebugRun{
		conversationID:     request.Conversation.ID,
		assistantMessageID: request.AssistantMessage.ID,
	}
	return []callbacks.Handler{
		newGraphNodeDebugCallback(run),
		newGraphToolDebugCallback(run),
	}
}

func newGraphNodeDebugCallback(run graphDebugRun) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
			slog.Info("agent graph node started", graphDebugAttrs(run, info)...)
			return context.WithValue(ctx, graphDebugStartKey{}, time.Now())
		}).
		OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context {
			logGraphNodeCompletion(ctx, run, info, nil)
			return ctx
		}).
		OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
			if output != nil {
				output.Close()
			}
			logGraphNodeCompletion(ctx, run, info, nil)
			return ctx
		}).
		OnErrorFn(func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
			logGraphNodeCompletion(ctx, run, info, err)
			return ctx
		}).
		Build()
}

func newGraphToolDebugCallback(run graphDebugRun) callbacks.Handler {
	return callbackutils.NewHandlerHelper().
		ToolsNode(&callbackutils.ToolsNodeCallbackHandlers{
			OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *schema.Message) context.Context {
				if input == nil {
					return ctx
				}
				names := make([]string, 0, len(input.ToolCalls))
				ids := make([]string, 0, len(input.ToolCalls))
				for _, call := range input.ToolCalls {
					names = append(names, call.Function.Name)
					ids = append(ids, call.ID)
				}
				slog.Info("agent graph tool calls requested",
					"conversation_id", run.conversationID,
					"assistant_message_id", run.assistantMessageID,
					"tool_names", names,
					"tool_call_ids", ids,
				)
				return ctx
			},
			OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output []*schema.Message) context.Context {
				slog.Info("agent graph tool calls completed",
					"conversation_id", run.conversationID,
					"assistant_message_id", run.assistantMessageID,
					"tool_result_count", len(output),
				)
				return ctx
			},
		}).
		Handler()
}

func logGraphNodeCompletion(ctx context.Context, run graphDebugRun, info *callbacks.RunInfo, err error) {
	attributes := graphDebugAttrs(run, info)
	if started, ok := ctx.Value(graphDebugStartKey{}).(time.Time); ok {
		attributes = append(attributes, "duration_ms", time.Since(started).Milliseconds())
	}
	if err != nil {
		attributes = append(attributes, "error", err)
		slog.Info("agent graph node failed", attributes...)
		return
	}
	slog.Info("agent graph node completed", attributes...)
}

func graphDebugAttrs(run graphDebugRun, info *callbacks.RunInfo) []any {
	attributes := []any{
		"conversation_id", run.conversationID,
		"assistant_message_id", run.assistantMessageID,
	}
	if info == nil {
		return attributes
	}
	nodeName := info.Name
	if nodeName == "" {
		nodeName = "unnamed"
		if info.Component == "Graph" {
			nodeName = "root_graph"
		}
	}
	return append(attributes, "node", nodeName, "component", info.Component, "type", info.Type)
}
