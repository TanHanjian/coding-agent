package interview

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
)

// Preserve execution interfaces while ToolsNode reads the same frozen Info
// used for binding and counting, rather than re-querying dynamic tool metadata.
type frozenInfo struct{ info *schema.ToolInfo }

func (f frozenInfo) Info(context.Context) (*schema.ToolInfo, error) { return f.info, nil }

type frozenInvokable struct {
	frozenInfo
	source tool.InvokableTool
}

func (f frozenInvokable) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	return f.source.InvokableRun(ctx, args, opts...)
}

type frozenStreamable struct {
	frozenInfo
	source tool.StreamableTool
}

func (f frozenStreamable) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	return f.source.StreamableRun(ctx, args, opts...)
}

type frozenEnhancedInvokable struct {
	frozenInfo
	source tool.EnhancedInvokableTool
}

func (f frozenEnhancedInvokable) InvokableRun(ctx context.Context, args *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
	return f.source.InvokableRun(ctx, args, opts...)
}

type frozenEnhancedStreamable struct {
	frozenInfo
	source tool.EnhancedStreamableTool
}

func (f frozenEnhancedStreamable) StreamableRun(ctx context.Context, args *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	return f.source.StreamableRun(ctx, args, opts...)
}

func freezeTools(tools []tool.BaseTool, infos []*schema.ToolInfo) ([]tool.BaseTool, error) {
	if len(tools) != len(infos) {
		return nil, fmt.Errorf("%w: tool and info counts differ: %d tools, %d infos", chat.ErrContextCountingFailed, len(tools), len(infos))
	}

	result := make([]tool.BaseTool, 0, len(tools))
	for i, source := range tools {
		info := frozenInfo{info: infos[i]}
		invokable, hasInvoke := source.(tool.InvokableTool)
		streamable, hasStream := source.(tool.StreamableTool)
		enhancedInvokable, hasEnhancedInvoke := source.(tool.EnhancedInvokableTool)
		enhancedStreamable, hasEnhancedStream := source.(tool.EnhancedStreamableTool)

		// ToolsNode registers invoke and stream independently, then prefers
		// enhanced execution. Preserve exactly the source's interface set so
		// its fallback conversions and precedence remain unchanged. Go cannot
		// implement both plain and enhanced signatures for the same method.
		// The outer frozenInfo resolves the embedded wrappers' Info methods.
		switch {
		case hasInvoke && hasStream:
			result = append(result, struct {
				frozenInfo
				frozenInvokable
				frozenStreamable
			}{info, frozenInvokable{info, invokable}, frozenStreamable{info, streamable}})
		case hasEnhancedInvoke && hasEnhancedStream:
			result = append(result, struct {
				frozenInfo
				frozenEnhancedInvokable
				frozenEnhancedStreamable
			}{info, frozenEnhancedInvokable{info, enhancedInvokable}, frozenEnhancedStreamable{info, enhancedStreamable}})
		case hasInvoke && hasEnhancedStream:
			result = append(result, struct {
				frozenInfo
				frozenInvokable
				frozenEnhancedStreamable
			}{info, frozenInvokable{info, invokable}, frozenEnhancedStreamable{info, enhancedStreamable}})
		case hasEnhancedInvoke && hasStream:
			result = append(result, struct {
				frozenInfo
				frozenEnhancedInvokable
				frozenStreamable
			}{info, frozenEnhancedInvokable{info, enhancedInvokable}, frozenStreamable{info, streamable}})
		case hasInvoke:
			result = append(result, frozenInvokable{info, invokable})
		case hasStream:
			result = append(result, frozenStreamable{info, streamable})
		case hasEnhancedInvoke:
			result = append(result, frozenEnhancedInvokable{info, enhancedInvokable})
		case hasEnhancedStream:
			result = append(result, frozenEnhancedStreamable{info, enhancedStreamable})
		default:
			return nil, fmt.Errorf("%w: tool execution interface is unsupported", chat.ErrContextCountingFailed)
		}
	}
	return result, nil
}
