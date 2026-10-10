package eino

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

type projectedContextStub struct {
	prepared chat.PreparedContext
	input    chat.ContextInput
}

func (s *projectedContextStub) Prepare(_ context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	s.input = input
	return s.prepared, nil
}

func TestExecutorUsesProjectedHistoryAtBothModelBoundaries(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{true: "empty", false: "facts"}[empty], func(t *testing.T) {
			history := []conversation.ContextMessage{}
			if !empty {
				history = append(history, conversation.ContextMessage{Role: conversation.MessageRoleAssistant, Content: "Safe tool facts, not executable", DowngradePolicy: conversation.ToolHistoryPolicyVersion, Sources: []conversation.ProjectionSource{{EntryID: "entry", PayloadVersion: 1, ProjectionVersion: conversation.ContextProjectionVersion, PolicyVersion: "v1", CallID: "call", ToolName: "search"}}})
			}
			manager := &projectedContextStub{prepared: chat.PreparedContext{ModelHistory: history, Query: "current"}}
			runtime := &runtimeStub{reader: schema.StreamReaderFromArray([]*schema.Message{})}
			builder := &builderStub{runtime: runtime}
			executor, err := NewExecutor(builder, WithContextManager(manager))
			if err != nil {
				t.Fatal(err)
			}
			req := testRequest([]conversation.Message{{Role: conversation.MessageRoleUser, Content: "wrong legacy"}})
			req.RunID = "run"
			if err := executor.Stream(context.Background(), req, &sinkStub{}); err != nil {
				t.Fatal(err)
			}
			if manager.input.RunID != req.RunID || builder.input.ModelHistory == nil || builder.input.History != nil || len(runtime.input.History) != len(history) || runtime.input.Query != "current" {
				t.Fatalf("build=%+v runtime=%+v", builder.input, runtime.input)
			}
			for _, message := range runtime.input.History {
				if len(message.ToolCalls) != 0 || message.Role == schema.Tool || message.Extra["context_sources"] == nil {
					t.Fatalf("message = %+v", message)
				}
			}
		})
	}
}

func TestExecutorRejectsInvalidProjectionBeforeBuild(t *testing.T) {
	manager := &projectedContextStub{prepared: chat.PreparedContext{ModelHistory: []conversation.ContextMessage{{Role: conversation.MessageRoleAssistant, Content: "no source"}}}}
	builder := &builderStub{}
	executor, err := NewExecutor(builder, WithContextManager(manager))
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Stream(context.Background(), testRequest(nil), &sinkStub{}); err == nil {
		t.Fatal("invalid projection reached builder")
	}
}
