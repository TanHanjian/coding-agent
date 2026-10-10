package interview

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	chat "interview-memory-agent/backend/internal/application/chat"
)

type frozenToolProbe struct {
	infoCalls int
	calls     []string
	ctx       context.Context
	args      string
	argument  *schema.ToolArgument
	opts      []tool.Option
	err       error
	result    *schema.ToolResult
	stream    *schema.StreamReader[string]
	enhanced  *schema.StreamReader[*schema.ToolResult]
}

func (p *frozenToolProbe) Info(context.Context) (*schema.ToolInfo, error) {
	p.infoCalls++
	return &schema.ToolInfo{Name: "dynamic_source"}, nil
}

func (p *frozenToolProbe) record(ctx context.Context, method, args string, opts []tool.Option) {
	p.calls = append(p.calls, method)
	p.ctx, p.args, p.opts = ctx, args, opts
}

func frozenProbeResult(text string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: text}}}
}

type frozenPlainInvokeProbe struct{ *frozenToolProbe }

func (p frozenPlainInvokeProbe) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	p.record(ctx, "plain invoke", args, opts)
	if p.err != nil {
		return "", p.err
	}
	return "plain invoke", nil
}

type frozenPlainStreamProbe struct{ *frozenToolProbe }

func (p frozenPlainStreamProbe) StreamableRun(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
	p.record(ctx, "plain stream", args, opts)
	if p.err != nil {
		return nil, p.err
	}
	p.stream = schema.StreamReaderFromArray([]string{"plain stream 1", "plain stream 2"})
	return p.stream, nil
}

type frozenEnhancedInvokeProbe struct{ *frozenToolProbe }

func (p frozenEnhancedInvokeProbe) InvokableRun(ctx context.Context, args *schema.ToolArgument, opts ...tool.Option) (*schema.ToolResult, error) {
	p.record(ctx, "enhanced invoke", args.Text, opts)
	p.argument = args
	if p.err != nil {
		return nil, p.err
	}
	p.result = frozenProbeResult("enhanced invoke")
	return p.result, nil
}

type frozenEnhancedStreamProbe struct{ *frozenToolProbe }

func (p frozenEnhancedStreamProbe) StreamableRun(ctx context.Context, args *schema.ToolArgument, opts ...tool.Option) (*schema.StreamReader[*schema.ToolResult], error) {
	p.record(ctx, "enhanced stream", args.Text, opts)
	p.argument = args
	if p.err != nil {
		return nil, p.err
	}
	p.enhanced = schema.StreamReaderFromArray([]*schema.ToolResult{
		frozenProbeResult("enhanced stream 1"), frozenProbeResult("enhanced stream 2"),
	})
	return p.enhanced, nil
}

// Concrete embeddings plus the outer probe avoid ambiguous Info promotion.
func frozenProbeSource(kind string, p *frozenToolProbe) tool.BaseTool {
	switch kind {
	case "plain dual":
		return struct {
			*frozenToolProbe
			frozenPlainInvokeProbe
			frozenPlainStreamProbe
		}{p, frozenPlainInvokeProbe{p}, frozenPlainStreamProbe{p}}
	case "enhanced dual":
		return struct {
			*frozenToolProbe
			frozenEnhancedInvokeProbe
			frozenEnhancedStreamProbe
		}{p, frozenEnhancedInvokeProbe{p}, frozenEnhancedStreamProbe{p}}
	case "plain invoke enhanced stream":
		return struct {
			*frozenToolProbe
			frozenPlainInvokeProbe
			frozenEnhancedStreamProbe
		}{p, frozenPlainInvokeProbe{p}, frozenEnhancedStreamProbe{p}}
	case "enhanced invoke plain stream":
		return struct {
			*frozenToolProbe
			frozenEnhancedInvokeProbe
			frozenPlainStreamProbe
		}{p, frozenEnhancedInvokeProbe{p}, frozenPlainStreamProbe{p}}
	case "plain invoke":
		return frozenPlainInvokeProbe{p}
	case "plain stream":
		return frozenPlainStreamProbe{p}
	case "enhanced invoke":
		return frozenEnhancedInvokeProbe{p}
	case "enhanced stream":
		return frozenEnhancedStreamProbe{p}
	default:
		panic("unknown probe kind: " + kind)
	}
}

var frozenProbeCases = []struct {
	kind   string
	invoke string
	stream string
}{
	{"plain dual", "plain invoke", "plain stream"},
	{"enhanced dual", "enhanced invoke", "enhanced stream"},
	// Eino fills the missing enhanced endpoint, then prefers enhanced for both modes.
	{"plain invoke enhanced stream", "enhanced stream", "enhanced stream"},
	{"enhanced invoke plain stream", "enhanced invoke", "enhanced invoke"},
	{"plain invoke", "plain invoke", "plain invoke"},
	{"plain stream", "plain stream", "plain stream"},
	{"enhanced invoke", "enhanced invoke", "enhanced invoke"},
	{"enhanced stream", "enhanced stream", "enhanced stream"},
}

func frozenProbeInterfaces(source tool.BaseTool) []bool {
	_, invoke := source.(tool.InvokableTool)
	_, stream := source.(tool.StreamableTool)
	_, enhancedInvoke := source.(tool.EnhancedInvokableTool)
	_, enhancedStream := source.(tool.EnhancedStreamableTool)
	return []bool{invoke, stream, enhancedInvoke, enhancedStream}
}

func TestFreezeToolsPreservesInterfacesAndDelegation(t *testing.T) {
	for _, tc := range frozenProbeCases {
		t.Run(tc.kind, func(t *testing.T) {
			p := &frozenToolProbe{}
			source := frozenProbeSource(tc.kind, p)
			snapshot := &schema.ToolInfo{Name: "frozen", Desc: "snapshot"}
			frozen, err := freezeTools([]tool.BaseTool{source}, []*schema.ToolInfo{snapshot})
			require.NoError(t, err)
			require.Len(t, frozen, 1)
			require.Equal(t, frozenProbeInterfaces(source), frozenProbeInterfaces(frozen[0]))
			for range 2 {
				info, err := frozen[0].Info(t.Context())
				require.NoError(t, err)
				require.Same(t, snapshot, info)
			}
			require.Zero(t, p.infoCalls)

			ctx := t.Context()
			args := &schema.ToolArgument{Text: `{"value":42}`}
			opts := []tool.Option{tool.WrapImplSpecificOptFn(func(o *int) { *o = 42 })}
			for _, sourceErr := range []error{nil, errors.New("source execution failed")} {
				p.err = sourceErr
				check := func(method string, err error) {
					t.Helper()
					require.Equal(t, sourceErr, err)
					require.Equal(t, method, p.calls[len(p.calls)-1])
					require.Equal(t, ctx, p.ctx)
					require.Equal(t, args.Text, p.args)
					require.Equal(t, 42, *tool.GetImplSpecificOptions(new(int), p.opts...))
				}
				if f, ok := frozen[0].(tool.InvokableTool); ok {
					output, err := f.InvokableRun(ctx, args.Text, opts...)
					check("plain invoke", err)
					if err == nil {
						require.Equal(t, "plain invoke", output)
					}
				}
				if f, ok := frozen[0].(tool.StreamableTool); ok {
					stream, err := f.StreamableRun(ctx, args.Text, opts...)
					check("plain stream", err)
					if err == nil {
						require.Same(t, p.stream, stream)
						for _, want := range []string{"plain stream 1", "plain stream 2"} {
							chunk, err := stream.Recv()
							require.NoError(t, err)
							require.Equal(t, want, chunk)
						}
						_, err = stream.Recv()
						require.ErrorIs(t, err, io.EOF)
						stream.Close()
					}
				}
				if f, ok := frozen[0].(tool.EnhancedInvokableTool); ok {
					output, err := f.InvokableRun(ctx, args, opts...)
					check("enhanced invoke", err)
					require.Same(t, args, p.argument)
					if err == nil {
						require.Same(t, p.result, output)
					}
				}
				if f, ok := frozen[0].(tool.EnhancedStreamableTool); ok {
					stream, err := f.StreamableRun(ctx, args, opts...)
					check("enhanced stream", err)
					require.Same(t, args, p.argument)
					if err == nil {
						require.Same(t, p.enhanced, stream)
						for _, want := range []string{"enhanced stream 1", "enhanced stream 2"} {
							chunk, err := stream.Recv()
							require.NoError(t, err)
							require.Equal(t, frozenProbeResult(want), chunk)
						}
						_, err = stream.Recv()
						require.ErrorIs(t, err, io.EOF)
						stream.Close()
					}
				}
			}
			require.Zero(t, p.infoCalls)
		})
	}
}

func TestFreezeToolsToolsNodeExecution(t *testing.T) {
	for _, tc := range frozenProbeCases {
		t.Run(tc.kind, func(t *testing.T) {
			p := &frozenToolProbe{}
			snapshot := &schema.ToolInfo{Name: "frozen"}
			frozen, err := freezeTools([]tool.BaseTool{frozenProbeSource(tc.kind, p)}, []*schema.ToolInfo{snapshot})
			require.NoError(t, err)
			node, err := compose.NewToolNode(t.Context(), &compose.ToolsNodeConfig{Tools: frozen})
			require.NoError(t, err)
			input := &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
				ID: "call_frozen", Type: "function", Function: schema.FunctionCall{Name: snapshot.Name, Arguments: `{"value":42}`},
			}}}
			output, err := node.Invoke(t.Context(), input)
			require.NoError(t, err)
			require.Len(t, output, 1)
			require.Equal(t, []string{tc.invoke}, p.calls)
			require.Equal(t, input.ToolCalls[0].Function.Arguments, p.args)
			require.Equal(t, "call_frozen", output[0].ToolCallID)
			require.Equal(t, snapshot.Name, output[0].ToolName)

			p.calls = nil
			stream, err := node.Stream(t.Context(), input)
			require.NoError(t, err)
			defer stream.Close()
			var chunks [][]*schema.Message
			for {
				chunk, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				chunks = append(chunks, chunk)
			}
			require.Equal(t, []string{tc.stream}, p.calls)
			require.Equal(t, input.ToolCalls[0].Function.Arguments, p.args)
			wantChunks := 1
			if tc.stream == "plain stream" || tc.stream == "enhanced stream" {
				wantChunks = 2
			}
			require.Len(t, chunks, wantChunks)
			for _, chunk := range chunks {
				require.Len(t, chunk, 1)
				require.Equal(t, "call_frozen", chunk[0].ToolCallID)
				require.Equal(t, snapshot.Name, chunk[0].ToolName)
			}
			require.Zero(t, p.infoCalls)
		})
	}
}

func TestFreezeToolsRejectsUnsupportedExecution(t *testing.T) {
	for _, source := range []tool.BaseTool{&frozenToolProbe{}, nil} {
		result, err := freezeTools([]tool.BaseTool{source}, []*schema.ToolInfo{{Name: "unsupported"}})
		require.Nil(t, result)
		require.ErrorIs(t, err, chat.ErrContextCountingFailed)
		require.EqualError(t, err, "model context could not be counted: tool execution interface is unsupported")
	}
}

func TestFreezeToolsRejectsInfoCountMismatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools []tool.BaseTool
		infos []*schema.ToolInfo
	}{
		{"missing infos", []tool.BaseTool{frozenPlainInvokeProbe{&frozenToolProbe{}}}, nil},
		{"partial infos", []tool.BaseTool{nil, nil}, []*schema.ToolInfo{{Name: "one"}}},
		{"extra infos", nil, []*schema.ToolInfo{{Name: "extra"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := freezeTools(tc.tools, tc.infos)
			require.Nil(t, result)
			require.ErrorIs(t, err, chat.ErrContextCountingFailed)
			require.Contains(t, err.Error(), "tool and info counts differ")
		})
	}
}

func TestFreezeToolsEmptyAndMultipleSnapshots(t *testing.T) {
	result, err := freezeTools(nil, nil)
	require.NoError(t, err)
	require.Empty(t, result)

	p := &frozenToolProbe{}
	infos := []*schema.ToolInfo{{Name: "first"}, {Name: "second"}}
	result, err = freezeTools([]tool.BaseTool{frozenPlainInvokeProbe{p}, frozenEnhancedStreamProbe{p}}, infos)
	require.NoError(t, err)
	require.Len(t, result, len(infos))
	for i, f := range result {
		info, err := f.Info(t.Context())
		require.NoError(t, err)
		require.Same(t, infos[i], info)
	}
	require.Zero(t, p.infoCalls)
}
