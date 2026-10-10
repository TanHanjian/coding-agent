package interview

import (
	"context"
	"reflect"
	"testing"

	"github.com/cloudwego/eino/schema"
	agenteino "interview-memory-agent/backend/internal/agent/eino"
	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

type graphProjectionManager struct{ history []conversation.ContextMessage }

func (m graphProjectionManager) Prepare(_ context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	return chat.PreparedContext{ModelHistory: m.history, Query: input.UserMessage.Content}, nil
}

type projectionSink struct{}

func (projectionSink) WriteChunk(context.Context, string) error               { return nil }
func (projectionSink) WriteEvent(context.Context, chat.GenerationEvent) error { return nil }

func TestProjectionSurvivesRealGraphToModel(t *testing.T) {
	chatModel := &scriptedToolCallingModel{plainTextOnly: true}
	builder, err := NewBuilder(chatModel, testBudgetGuard(t))
	if err != nil {
		t.Fatal(err)
	}
	history := []conversation.ContextMessage{
		{Role: conversation.MessageRoleUser, Content: "old user", Sources: []conversation.ProjectionSource{{MessageID: "m1", ProjectionVersion: conversation.ContextProjectionVersion}}},
		{Role: conversation.MessageRoleAssistant, Content: "safe closed group", DowngradePolicy: conversation.ToolHistoryPolicyVersion, Truncated: true, Sources: []conversation.ProjectionSource{{EntryID: "e2", PayloadVersion: 1, ProjectionVersion: conversation.ContextProjectionVersion, PolicyVersion: "v1", CallID: "c1", CallEntryID: "e1", ToolName: "search", IsError: true, Truncated: true}}},
	}
	executor, err := agenteino.NewExecutor(builder, agenteino.WithContextManager(graphProjectionManager{history: history}))
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.Stream(t.Context(), chat.Request{UserMessage: conversation.Message{Content: "current unique query"}, History: []conversation.Message{{Role: conversation.MessageRoleUser, Content: "wrong legacy"}}}, projectionSink{}); err != nil {
		t.Fatal(err)
	}
	if len(chatModel.inputs) != 1 {
		t.Fatalf("model calls = %d", len(chatModel.inputs))
	}
	var current, projected int
	for _, message := range chatModel.inputs[0] {
		if message.Role == schema.User && message.Content == "current unique query" {
			current++
		}
		if message.Content == "wrong legacy" {
			t.Fatal("legacy bypassed context manager")
		}
		for _, source := range history {
			if message.Content != source.Content {
				continue
			}
			projected++
			if !reflect.DeepEqual(message.Extra["context_sources"], source.Sources) || message.Extra["context_truncated"] != source.Truncated || message.Extra["context_downgrade_policy"] != source.DowngradePolicy || len(message.ToolCalls) != 0 || message.ToolCallID != "" {
				t.Fatalf("model lost provenance/protocol = %+v", message)
			}
		}
	}
	if current != 1 || projected != len(history) {
		t.Fatalf("current=%d projected=%d model=%+v", current, projected, chatModel.inputs[0])
	}
}

func TestPromptVariablesSelectsProjectionWithoutLegacyFallback(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{true: "empty", false: "facts"}[empty], func(t *testing.T) {
			history := []conversation.ContextMessage{}
			if !empty {
				history = append(history, conversation.ContextMessage{Role: conversation.MessageRoleAssistant, Content: "safe facts", Sources: []conversation.ProjectionSource{{EntryID: "e1", PayloadVersion: 1, ProjectionVersion: conversation.ContextProjectionVersion}}})
			}
			variables, err := promptVariables(chat.BuildInput{ModelHistory: history, History: []conversation.Message{{Role: "invalid", Content: "must not inspect legacy"}}, UserMessage: conversation.Message{Content: "current"}})
			if err != nil {
				t.Fatal(err)
			}
			projected := variables["history"].([]*schema.Message)
			if len(projected) != len(history) || variables["query"] != "current" {
				t.Fatalf("variables = %+v", variables)
			}
			if !empty && (projected[0].Content != "safe facts" || projected[0].Extra["context_sources"] == nil) {
				t.Fatalf("history = %+v", projected)
			}
		})
	}
	if _, err := promptVariables(chat.BuildInput{ModelHistory: []conversation.ContextMessage{{Role: conversation.MessageRoleAssistant, Content: "no source"}}}); err == nil {
		t.Fatal("invalid projection accepted")
	}
	if _, err := promptVariables(chat.BuildInput{History: []conversation.Message{{Role: "invalid"}}}); err == nil {
		t.Fatal("invalid legacy history accepted")
	}
}
