package interview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	agentprompt "interview-memory-agent/backend/internal/agent/prompt"
	chat "interview-memory-agent/backend/internal/application/chat"

	"github.com/cloudwego/eino/components/model"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// A3 measures the actual Graph request, then puts the unchanged baseline
// exactly on the input limit. Only the named prompt section grows in each case.
func TestBuilderGraphBudgetAcceptanceA3FinalPromptGrowth(t *testing.T) {
	const query = "review this interview"
	baseline := GraphInput{Query: query, ConversationSummary: "baseline summary", InterviewContext: "baseline material"}
	provider := budgetAcceptancePromptProvider("")
	probeModel := &budgetAcceptanceModel{}
	probeEstimator := newBudgetAcceptanceEstimator()
	probeRuntime := buildBudgetAcceptanceRuntime(t, probeModel, provider, newTestBudgetGuard(t, 1_000_000, probeEstimator))
	if output, err := readRuntimeStream(probeRuntime, baseline); err != nil || output != query {
		t.Fatalf("baseline probe output=%q error=%v", output, err)
	}
	probeCounts := probeEstimator.snapshot()
	probeInputs, _ := probeModel.snapshot()
	if len(probeCounts) != 1 || len(probeInputs) != 1 || !reflect.DeepEqual(probeCounts[0].messages, probeInputs[0]) || len(probeCounts[0].tools) != 0 {
		t.Fatal("baseline must count exactly the final model input without tool schemas")
	}
	available := probeCounts[0].count
	if available <= 0 {
		t.Fatalf("baseline count = %d; want positive input capacity", available)
	}

	growth := strings.TrimSpace(strings.Repeat("additional interview evidence ", 256))
	for _, section := range []string{"system_template", "ConversationSummary", "InterviewMaterial"} {
		t.Run(section, func(t *testing.T) {
			estimator := newBudgetAcceptanceEstimator()
			guard := newTestBudgetGuard(t, available, estimator)
			baselineModel := &budgetAcceptanceModel{}
			baselineRuntime := buildBudgetAcceptanceRuntime(t, baselineModel, provider, guard)
			if output, err := readRuntimeStream(baselineRuntime, baseline); err != nil || output != query {
				t.Fatalf("baseline at exact limit output=%q error=%v", output, err)
			}
			baselineInputs, _ := baselineModel.snapshot()
			if len(baselineInputs) != 1 || !reflect.DeepEqual(baselineInputs[0], probeInputs[0]) {
				t.Fatal("baseline model must receive the measured final input exactly once")
			}

			input := baseline
			grownProvider := provider
			switch section {
			case "system_template":
				grownProvider = budgetAcceptancePromptProvider(growth)
			case "ConversationSummary":
				input.ConversationSummary += growth
			case "InterviewMaterial":
				input.InterviewContext += growth
			}
			blockedModel := &budgetAcceptanceModel{}
			grownRuntime := buildBudgetAcceptanceRuntime(t, blockedModel, grownProvider, guard)
			output, err := readRuntimeStream(grownRuntime, input)
			if output != "" || !errors.Is(err, chat.ErrContextBudgetExceeded) {
				t.Fatalf("grown prompt output=%q error=%v; want empty output and budget rejection", output, err)
			}
			blockedInputs, _ := blockedModel.snapshot()
			counts := estimator.snapshot()
			if len(blockedInputs) != 0 || len(counts) != 2 {
				t.Fatalf("grown model calls=%d total counts=%d; want 0 calls and one count per run", len(blockedInputs), len(counts))
			}
			if counts[0].count != available || counts[1].count <= available {
				t.Fatalf("counts=%d,%d limit=%d; want baseline equal and growth above limit", counts[0].count, counts[1].count, available)
			}
			want := copyBudgetAcceptanceMessages(probeInputs[0])
			if section == "system_template" {
				want[0].Content += growth
			} else {
				want[1].Content = strings.Replace(want[1].Content, map[string]string{
					"ConversationSummary": baseline.ConversationSummary,
					"InterviewMaterial":   baseline.InterviewContext,
				}[section], map[string]string{
					"ConversationSummary": input.ConversationSummary,
					"InterviewMaterial":   input.InterviewContext,
				}[section], 1)
			}
			if !reflect.DeepEqual(counts[1].messages, want) || len(counts[1].tools) != 0 {
				t.Fatal("first rejected request must contain exactly the grown section in the final rendered prompt")
			}
		})
	}
}

// A15 runs the same compiled runtime and guard concurrently. Every run uses
// the full tool-round allowance, exposing shared histories or round counters.
func TestBuilderGraphBudgetAcceptanceA15ConcurrentRuns(t *testing.T) {
	const runs = 8
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	arrived := make(chan struct{}, runs)
	release := make(chan struct{})
	chatModel := &budgetAcceptanceModel{toolRounds: maxToolIterations, arrived: arrived, release: release}
	estimator := newBudgetAcceptanceEstimator()
	guard := newTestBudgetGuard(t, 1_000_000, estimator)
	currentTool, err := toolutils.InferTool[budgetAcceptanceToolInput, string](
		"search_questions", "Echo this run's query.",
		func(_ context.Context, input budgetAcceptanceToolInput) (string, error) { return input.Query, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(chatModel, guard, currentTool)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := builder.Build(ctx, chat.BuildInput{})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		query, output string
		err           error
	}
	results := make(chan result, runs)
	var workers sync.WaitGroup
	for run := 0; run < runs; run++ {
		query := fmt.Sprintf("concurrent-query-%02d", run)
		workers.Add(1)
		go func() {
			defer workers.Done()
			reader, err := runtime.Stream(ctx, GraphInput{Query: query})
			var output strings.Builder
			if err == nil {
				defer reader.Close()
				for {
					message, recvErr := reader.Recv()
					if errors.Is(recvErr, io.EOF) {
						break
					}
					if recvErr != nil {
						err = recvErr
						break
					}
					if message != nil {
						output.WriteString(message.Content)
					}
				}
			}
			results <- result{query: query, output: output.String(), err: err}
		}()
	}
	// All first model calls must be in flight before any tool loop proceeds.
	for run := 0; run < runs; run++ {
		select {
		case <-arrived:
		case <-ctx.Done():
			cancel()
			workers.Wait()
			t.Fatalf("concurrent first-call barrier: %v", ctx.Err())
		}
	}
	close(release)
	workers.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.output != result.query {
			t.Errorf("query=%q output=%q error=%v; want this run's final answer", result.query, result.output, result.err)
		}
	}

	inputs, boundTools := chatModel.snapshot()
	counts := estimator.snapshot()
	const callsPerRun = maxToolIterations + 1
	if len(inputs) != runs*callsPerRun || len(counts) != runs*callsPerRun || len(boundTools) != 1 {
		t.Fatalf("model calls=%d counts=%d bound tools=%d; want %d, %d, 1", len(inputs), len(counts), len(boundTools), runs*callsPerRun, runs*callsPerRun)
	}
	byQuery := make(map[string][][]*schema.Message)
	countByQuery := make(map[string][]budgetAcceptanceCount)
	for _, input := range inputs {
		query := budgetAcceptanceQuery(input)
		byQuery[query] = append(byQuery[query], input)
	}
	for _, count := range counts {
		query := budgetAcceptanceQuery(count.messages)
		countByQuery[query] = append(countByQuery[query], count)
	}
	if len(byQuery) != runs || len(countByQuery) != runs {
		t.Fatalf("distinct model/count runs=%d/%d; want %d each", len(byQuery), len(countByQuery), runs)
	}
	for run := 0; run < runs; run++ {
		query := fmt.Sprintf("concurrent-query-%02d", run)
		runInputs, runCounts := byQuery[query], countByQuery[query]
		if len(runInputs) != callsPerRun || len(runCounts) != callsPerRun {
			t.Fatalf("query=%q model calls=%d counts=%d; want %d each", query, len(runInputs), len(runCounts), callsPerRun)
		}
		for round, input := range runInputs {
			count := runCounts[round]
			if len(input) != 3+2*round || !reflect.DeepEqual(count.messages, input) || !reflect.DeepEqual(count.tools, boundTools) {
				t.Fatalf("query=%q round=%d: counted/model input, tool snapshot or private history differs", query, round)
			}
			wantCount, err := agentcontext.NewRequestTokenEstimator().CountMessages("graph-test-model", input, boundTools)
			if err != nil || count.count != wantCount || (round > 0 && count.count <= runCounts[round-1].count) {
				t.Fatalf("query=%q round=%d count=%d want=%d error=%v; want fresh growing request count", query, round, count.count, wantCount, err)
			}
			for toolRound := 0; toolRound < round; toolRound++ {
				assistant, result := input[3+2*toolRound], input[4+2*toolRound]
				wantCall := newSearchToolCall(fmt.Sprintf("%s-call-%d", query, toolRound), query)
				if assistant.Role != schema.Assistant || len(assistant.ToolCalls) != 1 || !reflect.DeepEqual(assistant.ToolCalls[0], wantCall) || result.Role != schema.Tool || result.ToolCallID != wantCall.ID || !strings.Contains(result.Content, query) {
					t.Fatalf("query=%q round=%d tool round=%d: another run's call/result leaked", query, round, toolRound)
				}
			}
		}
	}
}

func budgetAcceptancePromptProvider(systemGrowth string) agentprompt.Provider {
	return agentprompt.ProviderFunc(func(ctx context.Context, request agentprompt.Request) (agentprompt.ResolvedPrompt, error) {
		resolved, err := agentprompt.NewLocalPromptProvider().Resolve(ctx, request)
		if err == nil && systemGrowth != "" {
			message := *resolved.Templates[0].(*schema.Message)
			message.Content += systemGrowth
			resolved.Templates[0] = &message
		}
		return resolved, err
	})
}

func buildBudgetAcceptanceRuntime(t *testing.T, chatModel model.ToolCallingChatModel, provider agentprompt.Provider, guard *agentcontext.ModelCallGuard) chat.Runtime {
	t.Helper()
	builder, err := NewBuilderWithPromptProvider(chatModel, provider, guard)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := builder.Build(t.Context(), chat.BuildInput{})
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type budgetAcceptanceCount struct {
	messages []*schema.Message
	tools    []*schema.ToolInfo
	count    int
}

// The observer delegates both methods to the real request estimator. Its own
// lock protects counting and snapshots while the guard is shared by all runs.
type budgetAcceptanceEstimator struct {
	mu     sync.Mutex
	real   *agentcontext.RequestTokenEstimator
	counts []budgetAcceptanceCount
}

func newBudgetAcceptanceEstimator() *budgetAcceptanceEstimator {
	return &budgetAcceptanceEstimator{real: agentcontext.NewRequestTokenEstimator()}
}

func (e *budgetAcceptanceEstimator) CountText(modelID, text string) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.real.CountText(modelID, text)
}

func (e *budgetAcceptanceEstimator) CountMessages(modelID string, messages []*schema.Message, tools []*schema.ToolInfo) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	count, err := e.real.CountMessages(modelID, messages, tools)
	e.counts = append(e.counts, budgetAcceptanceCount{messages: copyBudgetAcceptanceMessages(messages), tools: append([]*schema.ToolInfo(nil), tools...), count: count})
	return count, err
}

func (e *budgetAcceptanceEstimator) snapshot() []budgetAcceptanceCount {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]budgetAcceptanceCount(nil), e.counts...)
}

type budgetAcceptanceToolInput struct {
	Query string `json:"query"`
}

// Responses depend only on this run's input, never a global invocation index.
// All shared observations are locked; the first-call barrier guarantees overlap.
type budgetAcceptanceModel struct {
	mu         sync.Mutex
	inputs     [][]*schema.Message
	tools      []*schema.ToolInfo
	toolRounds int
	arrived    chan<- struct{}
	release    <-chan struct{}
}

func (m *budgetAcceptanceModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = append([]*schema.ToolInfo(nil), tools...)
	return m, nil
}

func (m *budgetAcceptanceModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.reply(ctx, input)
}

func (m *budgetAcceptanceModel) Stream(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	message, err := m.reply(ctx, input)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{message}), nil
}

func (m *budgetAcceptanceModel) reply(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	m.mu.Lock()
	m.inputs = append(m.inputs, copyBudgetAcceptanceMessages(input))
	m.mu.Unlock()
	query := budgetAcceptanceQuery(input)
	round := 0
	for _, message := range input {
		if message.Role == schema.Tool {
			round++
		}
	}
	if round == 0 && m.arrived != nil {
		select {
		case m.arrived <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-m.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if round < m.toolRounds {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{newSearchToolCall(fmt.Sprintf("%s-call-%d", query, round), query)}}, nil
	}
	return schema.AssistantMessage(query, nil), nil
}

func (m *budgetAcceptanceModel) snapshot() ([][]*schema.Message, []*schema.ToolInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]*schema.Message(nil), m.inputs...), append([]*schema.ToolInfo(nil), m.tools...)
}

func budgetAcceptanceQuery(messages []*schema.Message) string {
	for _, message := range messages {
		if message.Role == schema.User {
			return message.Content
		}
	}
	return ""
}

func copyBudgetAcceptanceMessages(messages []*schema.Message) []*schema.Message {
	copies := make([]*schema.Message, len(messages))
	for i, message := range messages {
		copy := *message
		copy.ToolCalls = append([]schema.ToolCall(nil), message.ToolCalls...)
		copies[i] = &copy
	}
	return copies
}
