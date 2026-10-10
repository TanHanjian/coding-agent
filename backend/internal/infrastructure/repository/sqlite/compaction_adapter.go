package sqlite

import (
	"context"

	"interview-memory-agent/backend/internal/domain/conversation"
)

// CompactionAdapter 适配 EntryTree 为 CompactionWriter/Reader
type CompactionAdapter struct {
	entryTree conversation.EntryTree
}

func NewCompactionAdapter(tree conversation.EntryTree) *CompactionAdapter {
	return &CompactionAdapter{entryTree: tree}
}

// AppendCompaction 实现 CompactionWriter 接口
func (a *CompactionAdapter) AppendCompaction(ctx context.Context, input conversation.CompactionInput) (conversation.EntryID, error) {
	// 转换为 EntryTree.AppendCompaction 的输入
	treeInput := conversation.AppendCompactionInput{
		ConversationID: input.ConversationID,
		ExpectedHead: conversation.HeadToken{
			ActiveLeafID: &input.ExpectedLeaf,
			Version:      int64(input.ExpectedVersion),
		},
		Compaction: conversation.EntryCompaction{
			Schema:           "structured-v1",
			FirstKeptEntryID: input.FirstKeptEntryID,
			SourceBoundary:   []conversation.EntryID{input.ParentID},
			TokensBefore:     input.TokensBefore,
			Summary:          input.Summary,
		},
	}

	result, err := a.entryTree.AppendCompaction(ctx, treeInput)
	if err != nil {
		return "", err
	}

	return result.Entry.ID, nil
}

// ReadLatestCompaction 实现 CompactionReader 接口
func (a *CompactionAdapter) ReadLatestCompaction(ctx context.Context, query conversation.CompactionQuery) (*conversation.CompactionEntry, error) {
	// 读取完整路径
	path, err := a.entryTree.ReadPath(ctx, conversation.PathQuery{
		ConversationID: query.ConversationID,
		UseActiveLeaf:  true,
	})
	if err != nil {
		return nil, err
	}

	// 从后往前找最新的 compaction entry
	for i := len(path.Entries) - 1; i >= 0; i-- {
		entry := path.Entries[i]
		if entry.Compaction == nil {
			continue
		}

		// 验证 firstKeptEntryID 仍在当前路径上
		if !isOnPath(entry.Compaction.FirstKeptEntryID, query.OnPath) {
			continue
		}

		// 找到有效的 compaction
		parentID := entry.ID
		if entry.ParentID != nil {
			parentID = *entry.ParentID
		}
		return &conversation.CompactionEntry{
			ID:               entry.ID,
			ConversationID:   query.ConversationID,
			ParentID:         parentID,
			Kind:             string(conversation.EntryKindCompaction),
			PayloadVersion:   1,
			FirstKeptEntryID: entry.Compaction.FirstKeptEntryID,
			TokensBefore:     entry.Compaction.TokensBefore,
			Summary:          entry.Compaction.Summary,
			CreatedAt:        entry.CreatedAt,
		}, nil
	}

	// 没有找到有效的 compaction
	return nil, &conversation.EntryError{
		Code:   conversation.EntryErrCodeNotFound,
		Reason: "no valid compaction found",
	}
}

// isOnPath 检查 entryID 是否在给定路径中
func isOnPath(entryID conversation.EntryID, path []conversation.EntryID) bool {
	for _, id := range path {
		if id == entryID {
			return true
		}
	}
	return false
}

var _ conversation.CompactionWriter = (*CompactionAdapter)(nil)
var _ conversation.CompactionReader = (*CompactionAdapter)(nil)
