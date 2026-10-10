package agentcontext

import (
	"context"
	"errors"
	"fmt"

	chat "interview-memory-agent/backend/internal/application/chat"
	"interview-memory-agent/backend/internal/domain/conversation"
)

// prepareWithCompaction 实现基于 Projection 的压缩流程
func (m *Manager) prepareWithCompaction(ctx context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
	// 1. 读取 projection
	snapshot, err := m.projectionReader.ReadProjection(ctx, conversation.ProjectionQuery{
		ConversationID: input.Conversation.ID,
		RunID:          string(input.RunID),
		UserMessageID:  input.UserMessage.ID,
	})
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("read context projection: %w", err)
	}

	// 2. 规划预算
	plan, err := m.budgetPolicy.Plan(ctx, input)
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("plan context budget: %w", err)
	}

	// 3. 估算当前 token
	currentTokens := estimateSnapshotTokens(snapshot.Entries)

	// 4. 检查是否需要压缩
	if currentTokens <= plan.HistoryTokens {
		// 在预算内，直接返回
		history, err := ProjectSnapshot(snapshot)
		if err != nil {
			return chat.PreparedContext{}, fmt.Errorf("project context history: %w", err)
		}
		return chat.PreparedContext{
			ModelHistory:     conversation.CloneContextMessages(history),
			Query:            input.UserMessage.Content,
			InterviewContext: input.InterviewContext,
			Metadata: chat.ContextMetadata{
				HistoryIncluded:      len(history),
				ProjectionUsed:       true,
				LegacyBeforeSequence: snapshot.LegacyBeforeSequence,
			},
		}, nil
	}

	// 5. 选择压缩边界
	projectedHistory, err := ProjectSnapshot(snapshot)
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("project snapshot: %w", err)
	}

	boundary, err := m.boundarySelector.SelectPrefix(ctx, BoundaryInput{
		Snapshot: ProjectionSnapshot{
			Messages: projectedHistory,
		},
		BaseEntryID:     conversation.EntryID(""), // ProjectionSnapshot 中没有 BaseEntryID
		AvailableTokens: plan.HistoryTokens,
		CurrentEstimate: currentTokens,
		Estimator:       m.tokenEstimator,
	})
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("select compaction boundary: %w", err)
	}

	if !boundary.NeedsCompaction {
		// 不需要压缩
		return chat.PreparedContext{
			ModelHistory:     conversation.CloneContextMessages(projectedHistory),
			Query:            input.UserMessage.Content,
			InterviewContext: input.InterviewContext,
			Metadata: chat.ContextMetadata{
				HistoryIncluded:      len(projectedHistory),
				ProjectionUsed:       true,
				LegacyBeforeSequence: snapshot.LegacyBeforeSequence,
			},
		}, nil
	}

	// 6. 检查是否有可复用的 compaction
	pathIDs := extractEntryIDsFromEntries(snapshot.Entries)
	existingCompaction, err := m.compactionReader.ReadLatestCompaction(ctx, conversation.CompactionQuery{
		ConversationID: input.Conversation.ID,
		OnPath:         pathIDs,
	})
	// 使用结构化错误检查
	if err != nil {
		var entryErr *conversation.EntryError
		if errors.As(err, &entryErr) && entryErr.Code != conversation.EntryErrCodeNotFound {
			return chat.PreparedContext{}, fmt.Errorf("read compaction: %w", err)
		}
		// not_found 是预期的，继续
	}

	// 7. 转换边界中的 entries 为 ConversationEntry 格式
	prefixEntries := convertMessagesToEntries(boundary.PrefixMessages)

	// 8. 生成或复用摘要
	summaryResult, err := m.summaryCoordinator.Prepare(ctx, SummaryInput{
		ConversationID:  input.Conversation.ID,
		ExistingSummary: convertCompactionEntryToSummaryRecord(existingCompaction),
		OmittedHistory:  prefixEntries,
		BudgetTokens:    plan.SummaryTokens,
	})
	if err != nil {
		return chat.PreparedContext{}, fmt.Errorf("prepare summary: %w", err)
	}

	// 9. 如果生成了新摘要，持久化为 compaction entry
	if summaryResult.Updated && len(boundary.PrefixMessages) > 0 {
		// 找到前缀的最后一个 entry ID 作为 parentID
		lastPrefixMsg := boundary.PrefixMessages[len(boundary.PrefixMessages)-1]
		parentID := extractFirstSourceID(lastPrefixMsg)

		var expectedLeaf conversation.EntryID
		if snapshot.Head.ActiveLeafID != nil {
			expectedLeaf = *snapshot.Head.ActiveLeafID
		}

		_, err := m.compactionWriter.AppendCompaction(ctx, conversation.CompactionInput{
			ConversationID:   input.Conversation.ID,
			ParentID:         parentID,
			FirstKeptEntryID: boundary.FirstKeptEntryID,
			TokensBefore:     boundary.PrefixTokens,
			Summary:          convertContextSummaryToCompactionSummary(summaryResult.Summary.Summary),
			ExpectedLeaf:     expectedLeaf,
			ExpectedVersion:  int(snapshot.Head.Version),
		})
		if err != nil {
			// CAS 失败不是致命错误，可以继续使用摘要（未持久化）
			var entryErr *conversation.EntryError
			if !errors.As(err, &entryErr) || entryErr.Code != conversation.EntryErrCodeConflict {
				return chat.PreparedContext{}, fmt.Errorf("append compaction: %w", err)
			}
			// CAS 冲突时记录日志但继续
		}
	}

	// 10. 返回带摘要的上下文
	return chat.PreparedContext{
		ModelHistory:        conversation.CloneContextMessages(boundary.KeptMessages),
		Query:               input.UserMessage.Content,
		InterviewContext:    input.InterviewContext,
		ConversationSummary: convertSummaryRecordToString(summaryResult.Summary),
		Metadata: chat.ContextMetadata{
			HistoryIncluded:      len(boundary.KeptMessages),
			ProjectionUsed:       true,
			SummaryUsed:          summaryResult.Summary != nil,
			LegacyBeforeSequence: snapshot.LegacyBeforeSequence,
		},
	}, nil
}

// 辅助函数：从 snapshot entries 中提取所有 entry IDs
func extractEntryIDsFromEntries(entries []conversation.ConversationEntry) []conversation.EntryID {
	ids := make([]conversation.EntryID, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

// 辅助函数：将 ConversationEntry 转换为 ContextMessage
func convertEntriesToContextMessages(entries []conversation.ConversationEntry) []conversation.ContextMessage {
	messages := make([]conversation.ContextMessage, 0, len(entries))
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		var content string
		for _, part := range entry.Message.Content {
			if part.Text != nil {
				content += *part.Text
			}
		}
		role := conversation.MessageRoleAssistant
		if entry.Message.Role == conversation.EntryRoleUser {
			role = conversation.MessageRoleUser
		}
		messages = append(messages, conversation.ContextMessage{
			Role:    role,
			Content: content,
			Sources: []conversation.ProjectionSource{{EntryID: entry.ID}},
		})
	}
	return messages
}

// 辅助函数：估算 snapshot entries 的 token 数
func estimateSnapshotTokens(entries []conversation.ConversationEntry) int {
	total := 0
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		for _, part := range entry.Message.Content {
			if part.Text != nil {
				total += len(*part.Text)
			}
		}
	}
	return (total + 3) / 4
}

// 辅助函数：将 ContextMessage 转换为 ConversationEntry
func convertMessagesToEntries(messages []conversation.ContextMessage) []conversation.ConversationEntry {
	entries := make([]conversation.ConversationEntry, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Sources) == 0 {
			continue
		}
		// 简化实现：只取第一个 source
		src := msg.Sources[0]

		// 转换 Content 为 EntryPart
		parts := []conversation.EntryPart{}
		if msg.Content != "" {
			textPtr := new(string)
			*textPtr = msg.Content
			parts = append(parts, conversation.EntryPart{Text: textPtr})
		}

		// 转换 Role
		var entryRole conversation.EntryRole
		if msg.Role == conversation.MessageRoleUser {
			entryRole = conversation.EntryRoleUser
		} else {
			entryRole = conversation.EntryRoleAssistant
		}

		entries = append(entries, conversation.ConversationEntry{
			ID: src.EntryID,
			Message: &conversation.EntryMessage{
				Role:    entryRole,
				Content: parts,
			},
		})
	}
	return entries
}

// 辅助函数：从 ContextMessage 的 sources 中提取第一个 EntryID
func extractFirstSourceID(msg conversation.ContextMessage) conversation.EntryID {
	if len(msg.Sources) > 0 {
		return msg.Sources[0].EntryID
	}
	return conversation.EntryID("")
}

// 辅助函数：将 CompactionEntry 转换为 ContextSummaryRecord
func convertCompactionEntryToSummaryRecord(compaction *conversation.CompactionEntry) *ContextSummaryRecord {
	if compaction == nil {
		return nil
	}
	return &ContextSummaryRecord{
		ConversationID: compaction.ConversationID,
		SchemaVersion:  compaction.Summary.Version,
		Summary: ContextSummary{
			Goal:           compaction.Summary.Goal,
			ConfirmedFacts: compaction.Summary.ConfirmedFacts,
			Decisions:      compaction.Summary.Decisions,
			Constraints:    compaction.Summary.Constraints,
			OpenQuestions:  compaction.Summary.OpenQuestions,
		},
	}
}

// 辅助函数：将 ContextSummary 转换为 CompactionSummary
func convertContextSummaryToCompactionSummary(summary ContextSummary) conversation.CompactionSummary {
	return conversation.CompactionSummary{
		Version:        1,
		Goal:           summary.Goal,
		ConfirmedFacts: summary.ConfirmedFacts,
		Decisions:      summary.Decisions,
		Constraints:    summary.Constraints,
		OpenQuestions:  summary.OpenQuestions,
	}
}

// 辅助函数：将 ContextSummaryRecord 转换为 string
func convertSummaryRecordToString(record *ContextSummaryRecord) string {
	if record == nil {
		return ""
	}
	// 格式化为文本摘要
	return formatContextSummary(record.Summary)
}

// 辅助函数：格式化 ContextSummary 为文本
func formatContextSummary(summary ContextSummary) string {
	var result string
	if summary.Goal != "" {
		result += "Goal: " + summary.Goal + "\n\n"
	}
	if len(summary.ConfirmedFacts) > 0 {
		result += "Confirmed Facts:\n"
		for _, fact := range summary.ConfirmedFacts {
			result += "- " + fact + "\n"
		}
		result += "\n"
	}
	if len(summary.Decisions) > 0 {
		result += "Decisions:\n"
		for _, decision := range summary.Decisions {
			result += "- " + decision + "\n"
		}
		result += "\n"
	}
	if len(summary.Constraints) > 0 {
		result += "Constraints:\n"
		for _, constraint := range summary.Constraints {
			result += "- " + constraint + "\n"
		}
		result += "\n"
	}
	if len(summary.OpenQuestions) > 0 {
		result += "Open Questions:\n"
		for _, question := range summary.OpenQuestions {
			result += "- " + question + "\n"
		}
	}
	return result
}
