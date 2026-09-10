package chat

import (
	"context"

	"interview-memory-agent/backend/internal/domain/domainerr"
)

// generationTextWriter 是文本提交的应用层写入器：一批文本先持久化，再广播给
// 当前 SSE 订阅者。它不维护缓存或定时策略。
type generationTextWriter struct {
	store              TurnStore
	hub                GenerationHub
	assistantMessageID string
}

func newGenerationTextWriter(store TurnStore, hub GenerationHub, assistantMessageID string) *generationTextWriter {
	return &generationTextWriter{store: store, hub: hub, assistantMessageID: assistantMessageID}
}

// CommitText 处理已经聚合完成的一批文本：先写 SQLite，再广播 Hub。
func (w *generationTextWriter) CommitText(ctx context.Context, textBatch string) error {
	if textBatch == "" {
		return nil
	}
	return domainerr.ErrNotImplemented
}
