package conversation

import (
	"context"
	"time"
)

// CompactionEntry 是压缩边界的持久化记录，作为 Entry 的一种 kind 存储
type CompactionEntry struct {
	ID               EntryID
	ConversationID   string
	ParentID         EntryID
	Kind             string // "compaction"
	PayloadVersion   int    // 1
	FirstKeptEntryID EntryID
	TokensBefore     int
	Summary          CompactionSummary // 引用 entry.go 中的定义
	CreatedAt        time.Time
}

// CompactionWriter 负责原子追加 Compaction Entry
type CompactionWriter interface {
	AppendCompaction(ctx context.Context, input CompactionInput) (EntryID, error)
}

// CompactionInput 是追加 Compaction Entry 的输入
type CompactionInput struct {
	ConversationID   string
	ParentID         EntryID
	FirstKeptEntryID EntryID
	TokensBefore     int
	Summary          CompactionSummary
	ExpectedLeaf     EntryID
	ExpectedVersion  int
}

// CompactionReader 负责读取最新的 Compaction Entry
type CompactionReader interface {
	ReadLatestCompaction(ctx context.Context, query CompactionQuery) (*CompactionEntry, error)
}

// CompactionQuery 是查询 Compaction Entry 的输入
type CompactionQuery struct {
	ConversationID string
	OnPath         []EntryID // active path，用于验证 firstKeptEntryID 仍在当前路径上
}

// ErrInvalidCompactionBoundary 表示 compaction 边界不在有效路径上
var ErrInvalidCompactionBoundary = &EntryError{
	Code:   EntryErrCodeInvalidInput,
	Reason: "compaction boundary is not on the active path",
}
