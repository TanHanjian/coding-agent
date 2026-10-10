package agentcontext

import (
	"context"
	"errors"
	"fmt"

	"interview-memory-agent/backend/internal/domain/conversation"
)

var (
	ErrCompactionNotFound = errors.New("compaction entry not found")
	ErrCompactionInvalid  = errors.New("compaction entry is invalid")
)

// CompactionWriter 将压缩摘要原子写入 Entry 表
type CompactionWriter struct {
	tree conversation.EntryTree
}

func NewCompactionWriter(tree conversation.EntryTree) *CompactionWriter {
	return &CompactionWriter{tree: tree}
}

// WriteCompactionInput 是写入压缩条目的输入
type WriteCompactionInput struct {
	ConversationID   string
	FirstKeptEntryID conversation.EntryID
	SourceBoundary   []conversation.EntryID
	TokensBefore     int
	Summary          conversation.CompactionSummary
	ExpectedHead     conversation.HeadToken
}

// WriteCompaction 原子写入一个 Compaction Entry（带 CAS）
func (w *CompactionWriter) WriteCompaction(ctx context.Context, input WriteCompactionInput) (conversation.EntryID, error) {
	compaction := conversation.EntryCompaction{
		Schema:           "structured-v1",
		FirstKeptEntryID: input.FirstKeptEntryID,
		SourceBoundary:   input.SourceBoundary,
		TokensBefore:     input.TokensBefore,
		Summary:          input.Summary,
	}

	appendInput := conversation.AppendCompactionInput{
		ConversationID: input.ConversationID,
		ExpectedHead:   input.ExpectedHead,
		Compaction:     compaction,
	}

	result, err := w.tree.AppendCompaction(ctx, appendInput)
	if err != nil {
		return "", fmt.Errorf("append compaction entry: %w", err)
	}

	return result.Entry.ID, nil
}

// CompactionReader 从 Entry 表中读取最新的 Compaction Entry
type CompactionReader struct {
	tree conversation.EntryTree
}

func NewCompactionReader(tree conversation.EntryTree) *CompactionReader {
	return &CompactionReader{tree: tree}
}

// ReadLatestCompaction 读取指定会话 active path 上最新的 Compaction Entry
func (r *CompactionReader) ReadLatestCompaction(ctx context.Context, conversationID string) (*conversation.EntryCompaction, error) {
	// 读取 active path
	path, err := r.tree.ReadPath(ctx, conversation.PathQuery{
		ConversationID: conversationID,
		UseActiveLeaf:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("read active path: %w", err)
	}

	// 从后向前查找第一个 compaction entry
	for i := len(path.Entries) - 1; i >= 0; i-- {
		entry := path.Entries[i]
		if entry.Compaction != nil {
			// 验证 schema
			if entry.Compaction.Schema != "structured-v1" {
				return nil, fmt.Errorf("%w: unsupported schema %s", ErrCompactionInvalid, entry.Compaction.Schema)
			}
			// 验证必填字段
			if entry.Compaction.FirstKeptEntryID == "" {
				return nil, fmt.Errorf("%w: missing FirstKeptEntryID", ErrCompactionInvalid)
			}
			if entry.Compaction.Summary.Goal == "" {
				return nil, fmt.Errorf("%w: missing summary.goal", ErrCompactionInvalid)
			}
			return entry.Compaction, nil
		}
	}

	return nil, ErrCompactionNotFound
}
