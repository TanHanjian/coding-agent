package agentcontext

import (
	"context"
	"errors"
)

var ErrSummaryStoreNotImplemented = errors.New("agent context: summary store is not implemented")

// SummaryStore persists the latest structured summary for one conversation.
type SummaryStore interface {
	Get(context.Context, string) (ContextSummaryRecord, error)
	Upsert(context.Context, ContextSummaryRecord) error
}

// SummaryStoreAdapter is a function-based adapter for tests and incremental
// wiring. It contains no storage implementation.
type SummaryStoreAdapter struct {
	GetFunc    func(context.Context, string) (ContextSummaryRecord, error)
	UpsertFunc func(context.Context, ContextSummaryRecord) error
}

func (a SummaryStoreAdapter) Get(ctx context.Context, conversationID string) (ContextSummaryRecord, error) {
	return a.GetFunc(ctx, conversationID)
}

func (a SummaryStoreAdapter) Upsert(ctx context.Context, record ContextSummaryRecord) error {
	return a.UpsertFunc(ctx, record)
}
