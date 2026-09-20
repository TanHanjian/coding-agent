package sqlite

import (
	"context"

	agentcontext "interview-memory-agent/backend/internal/agent/context"
	"interview-memory-agent/backend/internal/infrastructure/storage"
)

// ContextSummaryRepository is the SQLite adapter for the agent context
// SummaryStore seam. SQL mapping and monotonic CoveredSequence handling are
// intentionally left for the implementation phase.
type ContextSummaryRepository struct {
	db *storage.DB
}

func NewContextSummaryRepository(db *storage.DB) *ContextSummaryRepository {
	return &ContextSummaryRepository{db: db}
}

func (*ContextSummaryRepository) Get(context.Context, string) (agentcontext.ContextSummaryRecord, error) {
	return agentcontext.ContextSummaryRecord{}, agentcontext.ErrSummaryStoreNotImplemented
}

func (*ContextSummaryRepository) Upsert(context.Context, agentcontext.ContextSummaryRecord) error {
	return agentcontext.ErrSummaryStoreNotImplemented
}

var _ agentcontext.SummaryStore = (*ContextSummaryRepository)(nil)
