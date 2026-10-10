package agentcontext

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
)

func budgetTestConfig() ModelCallConfig {
	return ModelCallConfig{ModelID: "test-model", ContextWindowTokens: 100, OutputLimitTokens: 20, SafetyMarginTokens: 10}
}

func budgetTestEstimator(count int, err error) TokenEstimator {
	return TokenEstimatorFunc{CountMessagesFunc: func(string, []*schema.Message, []*schema.ToolInfo) (int, error) { return count, err }}
}

func TestModelCallConfigValidation(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name   string
		config ModelCallConfig
		valid  bool
	}{
		{"normal", budgetTestConfig(), true},
		{"one input token", ModelCallConfig{"test", 2, 1, 0}, true},
		{"one with margin", ModelCallConfig{"test", 3, 1, 1}, true},
		{"empty model", ModelCallConfig{"", 100, 20, 10}, false},
		{"zero window", ModelCallConfig{"test", 0, 1, 0}, false},
		{"negative window", ModelCallConfig{"test", -1, 1, 0}, false},
		{"zero output", ModelCallConfig{"test", 100, 0, 0}, false},
		{"negative output", ModelCallConfig{"test", 100, -1, 0}, false},
		{"negative margin", ModelCallConfig{"test", 100, 20, -1}, false},
		{"output fills window", ModelCallConfig{"test", 100, 100, 0}, false},
		{"output exceeds window", ModelCallConfig{"test", 100, 101, 0}, false},
		{"margin fills remaining", ModelCallConfig{"test", 100, 20, 80}, false},
		{"margin exceeds remaining", ModelCallConfig{"test", 100, 20, 81}, false},
		{"max window", ModelCallConfig{"test", maxInt, 1, 0}, true},
		{"max output leaves one", ModelCallConfig{"test", maxInt, maxInt - 1, 0}, true},
		{"max margin leaves one", ModelCallConfig{"test", maxInt, 1, maxInt - 2}, true},
		{"overflowing output plus margin", ModelCallConfig{"test", maxInt, maxInt - 1, maxInt - 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid=%v", err, tc.valid)
			}
			guard, guardErr := NewModelCallGuard(tc.config, budgetTestEstimator(0, nil))
			if (guardErr == nil) != tc.valid || (guard != nil) != tc.valid {
				t.Fatalf("NewModelCallGuard() = %v, %v", guard, guardErr)
			}
			_, prepErr := PreparationBudget(tc.config)
			if (prepErr == nil) != tc.valid {
				t.Fatalf("PreparationBudget() = %v, valid=%v", prepErr, tc.valid)
			}
		})
	}
	if guard, err := NewModelCallGuard(budgetTestConfig(), nil); guard != nil || err == nil {
		t.Fatalf("nil estimator accepted: %v, %v", guard, err)
	}
}

func TestModelCallGuardBoundariesAndCountingFailures(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count       int
		estimateErr error
		want        error
	}{
		{"zero", 0, nil, nil},
		{"below boundary", 69, nil, nil},
		{"exact boundary", 70, nil, nil},
		{"boundary plus one", 71, nil, chat.ErrContextBudgetExceeded},
		{"negative count", -1, nil, chat.ErrContextCountingFailed},
		{"minimum int count", -int(^uint(0)>>1) - 1, nil, chat.ErrContextCountingFailed},
		{"estimation error", 0, errors.New("PRIVATE-ESTIMATOR-ERROR"), chat.ErrContextCountingFailed},
		{"error takes precedence over overflow", 1000, errors.New("PRIVATE-ESTIMATOR-ERROR"), chat.ErrContextCountingFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard, err := NewModelCallGuard(budgetTestConfig(), budgetTestEstimator(tc.count, tc.estimateErr))
			if err != nil {
				t.Fatal(err)
			}
			err = guard.Check(context.Background(), "chat", 1, []*schema.Message{schema.UserMessage("PRIVATE-INPUT")}, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Check() = %v, want %v", err, tc.want)
			}
			if err != nil && (strings.Contains(err.Error(), "PRIVATE") || errors.Is(err, tc.estimateErr)) {
				t.Fatalf("guard leaked or wrapped sensitive estimator error: %v", err)
			}
		})
	}
	var guard *ModelCallGuard
	if err := guard.Check(context.Background(), "summary", 1, nil, nil); !errors.Is(err, chat.ErrContextCountingFailed) {
		t.Fatalf("nil guard error = %v", err)
	}
}

func TestModelCallGuardForwardsCompleteRequest(t *testing.T) {
	messages := []*schema.Message{schema.UserMessage("input"), schema.AssistantMessage("response", nil)}
	tools := []*schema.ToolInfo{{Name: "tool"}}
	calls := 0
	estimator := TokenEstimatorFunc{CountMessagesFunc: func(model string, gotMessages []*schema.Message, gotTools []*schema.ToolInfo) (int, error) {
		calls++
		if model != "test-model" || len(gotMessages) != 2 || gotMessages[0] != messages[0] || gotMessages[1] != messages[1] || len(gotTools) != 1 || gotTools[0] != tools[0] {
			t.Fatal("guard did not forward model and completed request unchanged")
		}
		return 70, nil
	}}
	guard, err := NewModelCallGuard(budgetTestConfig(), estimator)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := guard.Check(context.Background(), "chat", i+1, messages, tools); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("guard skipped recounting: %d calls", calls)
	}
}

func TestModelCallGuardCancellation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		during      bool
		count       int
		estimateErr error
	}{
		{"before counting", false, 70, nil},
		{"during allowed count", true, 70, nil},
		{"during exceeded count", true, 71, nil},
		{"during counting error", true, -1, errors.New("private")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			estimator := TokenEstimatorFunc{CountMessagesFunc: func(string, []*schema.Message, []*schema.ToolInfo) (int, error) {
				calls++
				cancel()
				return tc.count, tc.estimateErr
			}}
			guard, err := NewModelCallGuard(budgetTestConfig(), estimator)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.during {
				cancel()
			}
			if err := guard.Check(ctx, "chat", 1, nil, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("Check() = %v, want context.Canceled", err)
			}
			wantCalls := 0
			if tc.during {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("estimator calls = %d, want %d", calls, wantCalls)
			}
		})
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	guard, err := NewModelCallGuard(budgetTestConfig(), TokenEstimatorFunc{CountMessagesFunc: func(string, []*schema.Message, []*schema.ToolInfo) (int, error) {
		t.Fatal("expired deadline must not call estimator")
		return 0, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Check(ctx, "summary", 1, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline error = %v", err)
	}
}

func TestModelCallGuardMaximumIntSafety(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, config := range []ModelCallConfig{
		{"test", maxInt, 1, 0},
		{"test", maxInt, maxInt - 1, 0},
		{"test", maxInt, 1, maxInt - 2},
		{"test", maxInt, maxInt / 2, maxInt / 4},
	} {
		available := config.ContextWindowTokens - config.OutputLimitTokens - config.SafetyMarginTokens
		for _, count := range []int{available, available + 1, maxInt} {
			guard, err := NewModelCallGuard(config, budgetTestEstimator(count, nil))
			if err != nil {
				t.Fatal(err)
			}
			err = guard.Check(context.Background(), "chat", 1, nil, nil)
			if count == available {
				if err != nil {
					t.Fatalf("exact maximum-int boundary rejected: %v", err)
				}
			} else if !errors.Is(err, chat.ErrContextBudgetExceeded) {
				t.Fatalf("count=%d available=%d: got %v", count, available, err)
			}
		}
	}
}

func TestModelCallGuardErrorsAndLogsDoNotLeak(t *testing.T) {
	// No parallel tests: slog's default logger is process-global.
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)
	config := budgetTestConfig()
	config.ModelID = "PRIVATE-MODEL-ID"
	guard, err := NewModelCallGuard(config, budgetTestEstimator(-1, errors.New("PRIVATE-ESTIMATOR-ERROR")))
	if err != nil {
		t.Fatal(err)
	}
	err = guard.Check(context.Background(), "PRIVATE-PHASE", 2, []*schema.Message{schema.UserMessage("PRIVATE-INPUT")}, []*schema.ToolInfo{{Name: "PRIVATE-TOOL"}})
	if !errors.Is(err, chat.ErrContextCountingFailed) {
		t.Fatalf("counting error = %v", err)
	}
	if strings.Contains(err.Error()+logs.String(), "PRIVATE") {
		t.Fatalf("sensitive request data leaked: error=%v logs=%s", err, logs.String())
	}
	if !strings.Contains(logs.String(), `"phase":"unknown"`) || !strings.Contains(logs.String(), `"reason":"counting_failed"`) {
		t.Fatalf("missing normalized diagnostics: %s", logs.String())
	}
}

func TestGuardedModelInvoker(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int
		countErr   error
		cancel     bool
		nilGuard   bool
		nilInvoker bool
		want       error
		wantCalls  int
	}{
		{"allowed", 70, nil, false, false, false, nil, 1},
		{"over budget", 71, nil, false, false, false, chat.ErrContextBudgetExceeded, 0},
		{"count failure", 0, errors.New("private"), false, false, false, chat.ErrContextCountingFailed, 0},
		{"negative", -1, nil, false, false, false, chat.ErrContextCountingFailed, 0},
		{"canceled", 70, nil, true, false, false, context.Canceled, 0},
		{"nil guard", 70, nil, false, true, false, chat.ErrContextCountingFailed, 0},
		{"nil invoker", 70, nil, false, false, true, chat.ErrContextCountingFailed, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := []*schema.Message{schema.SystemMessage("summary instructions"), schema.UserMessage("full summary input")}
			guard, err := NewModelCallGuard(budgetTestConfig(), TokenEstimatorFunc{CountMessagesFunc: func(_ string, got []*schema.Message, tools []*schema.ToolInfo) (int, error) {
				if len(got) != len(messages) || got[0] != messages[0] || got[1] != messages[1] || tools != nil {
					t.Fatal("summary guard must count full prompt without chat tools")
				}
				return tc.count, tc.countErr
			}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.nilGuard {
				guard = nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			calls := 0
			response := schema.AssistantMessage("local fake response", nil)
			var invoke func(context.Context, []*schema.Message) (*schema.Message, error)
			if !tc.nilInvoker {
				invoke = func(gotCtx context.Context, got []*schema.Message) (*schema.Message, error) {
					calls++
					if gotCtx != ctx || len(got) != 2 || got[0] != messages[0] || got[1] != messages[1] {
						t.Fatal("invoker request changed")
					}
					return response, nil
				}
			}
			got, err := GuardedModelInvoker(guard, invoke)(ctx, messages)
			if !errors.Is(err, tc.want) || calls != tc.wantCalls {
				t.Fatalf("response=%v err=%v calls=%d; want err=%v calls=%d", got, err, calls, tc.want, tc.wantCalls)
			}
			if tc.want == nil && got != response || tc.want != nil && got != nil {
				t.Fatalf("unexpected response: %v", got)
			}
		})
	}
	t.Run("invoker error preserved", func(t *testing.T) {
		guard, err := NewModelCallGuard(budgetTestConfig(), budgetTestEstimator(0, nil))
		if err != nil {
			t.Fatal(err)
		}
		wantErr := errors.New("fake model failure")
		_, err = GuardedModelInvoker(guard, func(context.Context, []*schema.Message) (*schema.Message, error) { return nil, wantErr })(context.Background(), nil)
		if err != wantErr {
			t.Fatalf("invoker error changed: %v", err)
		}
	})
}

func TestPreparationBudgetCapacityPartitionsAndSmallWindows(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	configs := []ModelCallConfig{
		{"test", 32000, 4000, 0},
		{"test", 32000, 4000, 123},
		{"test", maxInt, 1, 0},
		{"test", maxInt, maxInt - 1, 0},
		{"test", maxInt, 1, maxInt - 2},
	}
	// Exhaust all legal tiny windows, including sections rounded down to zero.
	for window := 2; window <= 25; window++ {
		for output := 1; output < window; output++ {
			for margin := 0; margin < window-output; margin++ {
				configs = append(configs, ModelCallConfig{"test", window, output, margin})
			}
		}
	}
	weights := []int64{4000, 2000, 8000, 6000, 8000}
	for _, config := range configs {
		budget, err := PreparationBudget(config)
		if err != nil {
			t.Fatalf("config=%+v: %v", config, err)
		}
		available := config.ContextWindowTokens - config.OutputLimitTokens - config.SafetyMarginTokens
		got := []int{budget.InstructionTokens, budget.SummaryTokens, budget.HistoryTokens, budget.InterviewMaterialTokens, budget.ToolResultTokens}
		// Big integer multiplication is an independent overflow-free ratio oracle.
		want := make([]int, len(weights))
		used := 0
		for i, weight := range weights {
			numerator := new(big.Int).Mul(big.NewInt(int64(available)), big.NewInt(weight))
			want[i] = int(numerator.Quo(numerator, big.NewInt(28000)).Int64())
			used += want[i]
		}
		want[2] += available - used
		sum := 0
		for i, section := range got {
			if section < 0 || section > available || section != want[i] {
				t.Fatalf("config=%+v partition[%d]=%d, want %d (available=%d)", config, i, section, want[i], available)
			}
			sum += section
		}
		if sum != available || budget.ContextWindowTokens != config.ContextWindowTokens || budget.OutputReservedTokens != config.OutputLimitTokens {
			t.Fatalf("capacity/output/margin mismatch: config=%+v budget=%+v sum=%d available=%d", config, budget, sum, available)
		}
	}
}

func TestModelCallGuardRejectsEstimatorPanic(t *testing.T) {
	guard, err := NewModelCallGuard(budgetTestConfig(), TokenEstimatorFunc{CountMessagesFunc: func(string, []*schema.Message, []*schema.ToolInfo) (int, error) {
		panic("PRIVATE estimator panic")
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = guard.Check(context.Background(), "chat", 1, nil, nil)
	if !errors.Is(err, chat.ErrContextCountingFailed) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("panic rejection = %v", err)
	}
}
