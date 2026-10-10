package agentcontext

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"interview-memory-agent/backend/internal/domain/conversation"
)

func TestEinoContextSummarizer_Summarize(t *testing.T) {
	t.Run("generates summary from history", func(t *testing.T) {
		// Mock model invoker that returns structured summary
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			summary := ContextSummary{
				Goal:           "Test user's goal",
				ConfirmedFacts: []string{"Fact 1", "Fact 2"},
				Decisions:      []string{"Decision A"},
				Constraints:    []string{"Constraint X"},
				OpenQuestions:  []string{"Question?"},
			}
			jsonBytes, _ := json.Marshal(summary)
			return &schema.Message{
				Role:    schema.Assistant,
				Content: string(jsonBytes),
			}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)

		text1 := "Hello"
		text2 := "I need help with X"
		input := SummaryPrompt{
			ConversationID: "conv1",
			History: []conversation.ConversationEntry{
				{
					Message: &conversation.EntryMessage{
						Role: conversation.EntryRoleUser,
						Content: []conversation.EntryPart{
							{Type: conversation.EntryPartText, Text: &text1},
						},
					},
				},
				{
					Message: &conversation.EntryMessage{
						Role: conversation.EntryRoleUser,
						Content: []conversation.EntryPart{
							{Type: conversation.EntryPartText, Text: &text2},
						},
					},
				},
			},
		}

		result, err := summarizer.Summarize(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, "Test user's goal", result.Goal)
		assert.Len(t, result.ConfirmedFacts, 2)
		assert.Len(t, result.Decisions, 1)
	})

	t.Run("updates existing summary", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			// Verify prompt mentions existing summary
			assert.Contains(t, messages[1].Content, "Existing summary")
			summary := ContextSummary{
				Goal:           "Updated goal",
				ConfirmedFacts: []string{"Old fact", "New fact"},
			}
			jsonBytes, _ := json.Marshal(summary)
			return &schema.Message{Content: string(jsonBytes)}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)

		input := SummaryPrompt{
			ExistingSummary: &ContextSummaryRecord{
				Summary: ContextSummary{Goal: "Old goal"},
			},
			History: []conversation.ConversationEntry{},
		}

		result, err := summarizer.Summarize(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, "Updated goal", result.Goal)
	})

	t.Run("returns error when model invocation fails", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			return nil, errors.New("model error")
		}

		summarizer := NewEinoContextSummarizer(invoker)
		_, err := summarizer.Summarize(context.Background(), SummaryPrompt{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "model invocation failed")
	})

	t.Run("returns error when response is empty", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: ""}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)
		_, err := summarizer.Summarize(context.Background(), SummaryPrompt{})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrSummarizationFailed)
	})

	t.Run("returns error when JSON is invalid", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			return &schema.Message{Content: "not json"}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)
		_, err := summarizer.Summarize(context.Background(), SummaryPrompt{})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrSummarizationFailed)
		assert.Contains(t, err.Error(), "invalid JSON")
	})

	t.Run("returns error when goal is missing", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			summary := ContextSummary{
				Goal: "", // Missing goal
			}
			jsonBytes, _ := json.Marshal(summary)
			return &schema.Message{Content: string(jsonBytes)}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)
		_, err := summarizer.Summarize(context.Background(), SummaryPrompt{})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrSummarizationFailed)
		assert.Contains(t, err.Error(), "missing goal")
	})

	t.Run("handles nil message in history", func(t *testing.T) {
		invoker := func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
			summary := ContextSummary{Goal: "Goal"}
			jsonBytes, _ := json.Marshal(summary)
			return &schema.Message{Content: string(jsonBytes)}, nil
		}

		summarizer := NewEinoContextSummarizer(invoker)

		input := SummaryPrompt{
			History: []conversation.ConversationEntry{
				{Message: nil},
				{Message: &conversation.EntryMessage{}},
			},
		}

		_, err := summarizer.Summarize(context.Background(), input)
		assert.NoError(t, err)
	})
}
