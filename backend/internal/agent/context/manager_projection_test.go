package agentcontext

import (
	"context"
	"errors"
	"testing"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

type projectionReaderStub struct {
	query    conversation.ProjectionQuery
	snapshot conversation.ProjectionSnapshot
	err      error
}

func (r *projectionReaderStub) ReadProjection(_ context.Context, query conversation.ProjectionQuery) (conversation.ProjectionSnapshot, error) {
	r.query = query
	return r.snapshot, r.err
}

func TestManagerProjectionBindingAndEmptyHistory(t *testing.T) {
	reader := &projectionReaderStub{snapshot: conversation.ProjectionSnapshot{Head: conversation.EntryHead{ConversationID: "conv"}, LegacyBeforeSequence: 1}}
	manager := NewManager(WithProjectionReader(reader))
	input := chat.ContextInput{Conversation: conversation.Conversation{ID: "conv"}, RunID: "run", UserMessage: conversation.Message{ID: "current", Content: "query"}, History: []conversation.Message{{Content: "must not fallback"}}, InterviewContext: "material"}
	prepared, err := manager.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if reader.query != (conversation.ProjectionQuery{ConversationID: "conv", RunID: "run", UserMessageID: "current"}) {
		t.Fatalf("query = %+v", reader.query)
	}
	if prepared.ModelHistory == nil || len(prepared.ModelHistory) != 0 || prepared.History != nil || prepared.Query != "query" || prepared.InterviewContext != "material" {
		t.Fatalf("prepared = %+v", prepared)
	}
	reader.err = errors.New("snapshot unavailable")
	if _, err := manager.Prepare(context.Background(), input); !errors.Is(err, reader.err) {
		t.Fatalf("read failure = %v", err)
	}
	if _, err := NewManager().Prepare(context.Background(), input); err == nil {
		t.Fatal("persisted run silently fell back")
	}
	reader.err = nil
	reader.snapshot = projectionFixture(t)
	reader.snapshot.Entries = reader.snapshot.Entries[:2]
	if _, err := manager.Prepare(context.Background(), input); err == nil {
		t.Fatal("pending group accepted")
	}
}
