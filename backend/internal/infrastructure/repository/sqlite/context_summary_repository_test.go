package sqlite

import (
	"context"
	"errors"
	"reflect"
	"testing"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
)

func TestContextSummaryRemainsUnimplemented(t *testing.T) {
	repo := NewContextSummaryRepository(nil)
	record, err := repo.Get(context.Background(), "existing-conversation")
	if !errors.Is(err, agentcontext.ErrSummaryStoreNotImplemented) || !reflect.DeepEqual(record, agentcontext.ContextSummaryRecord{}) {
		t.Fatalf("Get = %+v, %v", record, err)
	}
	if err := repo.Upsert(context.Background(), agentcontext.ContextSummaryRecord{}); !errors.Is(err, agentcontext.ErrSummaryStoreNotImplemented) {
		t.Fatalf("Upsert = %v", err)
	}
}
