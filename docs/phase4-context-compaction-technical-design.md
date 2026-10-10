# Phase 4 技术设计：Context Compaction 与预算协调

> 状态：已实现并验收。所属变更：[refactor-conversation-message-tree](../openspec/changes/refactor-conversation-message-tree/design.md)。
> 前置：[Phase 1](phase1-entry-tree-technical-design.md)、[Phase 2](phase2-agent-run-technical-design.md)、[Phase 3](phase3-context-projection-technical-design.md)。

## 1. 范围

Phase 4 实现上下文压缩（compaction）与预算协调，在 Phase 3 Projection 基础上增加模型窗口预算强制执行、合法边界选择、摘要生成与原子树提交，以及重启后的安全复用。

本阶段交付：

- **最终预算强制**：每次模型调用前验证输入估算、输出预留和安全余量总和不超过上下文窗口
- **合法边界选择**：以完整用户轮次和完整 assistant tool-call/toolResult 组为压缩边界，当前用户请求和未完成工具组必须保护
- **摘要生成与原子提交**：使用已选定快照生成结构化摘要，schema/预算/协议/CAS 全部成功后追加 compaction Entry
- **安全复用**：重启或后续 Run 中复用当前 active path 上合法的最新 compaction，校验 schema/边界/路径/预算
- **Legacy 摘要迁移**：旧线性 `covered_sequence` 摘要只通过显式 mapping 或失效重建进入新 Projection

**不实现**：

- 产品 Phase 3 长期记忆
- branch_summary、context_edit 新持久化类型
- system/media 投影
- 完整执行恢复或具名分支
- 原始工具协议重建
- artifact（属于独立可选能力，在 compaction 后另行批准）

Phase 4 属于产品 Phase 2，不阻塞 MVP 交付。

**实施完成情况**：

- ✅ 切片 1（commit 4e6c486）：预算强制、token 估算、边界选择
- ✅ 切片 2（commit f3f404c）：摘要生成、Compaction Entry 存储
- ✅ 切片 3（commit 7322701）：集成与复用
- ✅ 生产装配（commit 8d777cd）：组件注入 main.go
- ✅ 端到端测试（commit 46a57b1）：完整压缩流程验证

所有测试通过，go vet 无警告。真实模型端到端验证需要运行服务。

## 2. 数据流与模块

```text
ContextManager.Prepare
  -> BudgetPolicy.Plan (配置窗口、各区间预留)
  -> ProjectionReader.ReadProjection (快照路径)
  -> BoundarySelector.SelectPrefix (合法前缀边界)
  -> SummaryCoordinator.Prepare (决定生成/复用)
    -> ContextSummarizer.Summarize (LLM 生成摘要)
  -> CompactionWriter.AppendCompaction (原子提交 Entry + CAS)
  -> ProjectWithCompaction (摘要 + 保留后缀)
```

核心模块职责：

| Module | Responsibility | Port |
|---|---|---|
| `BudgetPolicy` | 模型窗口知识与各区间预留决策 | `agent/context/budget.go` |
| `BoundarySelector` | 完整组边界与合法切割点选择 | 新增 `agent/context/boundary.go` |
| `ContextSummarizer` | 调用模型生成结构化摘要 | 已有 `agent/context/summarizer.go` |
| `SummaryCoordinator` | 决定生成新摘要或复用现有摘要 | 已有 `agent/context/summary.go` |
| `CompactionWriter` | 原子 compaction Entry 提交与 CAS | 新增 `domain/conversation/compaction_store.go` |
| `CompactionReader` | 读取 active path 上合法的 compaction | 新增 `domain/conversation/compaction_store.go` |

## 3. 预算强制与估算

### 3.1 BudgetPolicy 接口

已有 `BudgetPolicy` 接口定义预算决策，`DefaultBudgetPolicy` 返回配置值并标记 `ErrBudgetPolicyNotImplemented`。

Phase 4 实现：

```go
type EnforcedBudgetPolicy struct {
    config         BudgetConfig
    tokenEstimator TokenEstimator
    modelWindows   map[string]int // modelID -> context window
}

func (p *EnforcedBudgetPolicy) Plan(ctx context.Context, input chat.ContextInput) (BudgetPlan, error) {
    window := p.modelWindows[input.ModelID]
    if window == 0 {
        return BudgetPlan{}, fmt.Errorf("unknown context window for model %s", input.ModelID)
    }
    
    plan := BudgetPlan{
        ContextWindowTokens:     window,
        InstructionTokens:       p.config.InstructionTokens,
        SummaryTokens:           p.config.SummaryTokens,
        HistoryTokens:           p.config.HistoryTokens,
        InterviewMaterialTokens: p.config.InterviewMaterialTokens,
        ToolResultTokens:        p.config.ToolResultTokens,
        OutputReservedTokens:    p.config.OutputReservedTokens,
    }
    
    total := plan.InstructionTokens + plan.SummaryTokens + plan.HistoryTokens +
             plan.InterviewMaterialTokens + plan.ToolResultTokens + plan.OutputReservedTokens
    if total > window {
        return BudgetPlan{}, fmt.Errorf("budget configuration exceeds context window: %d > %d", total, window)
    }
    
    return plan, nil
}
```

### 3.2 Token 估算

已有 `TokenEstimator` 接口，Phase 4 实现基于字符近似估算：

```go
type CharBasedTokenEstimator struct {
    charsPerToken float64 // 默认 4.0
}

func (e *CharBasedTokenEstimator) EstimateTokens(ctx context.Context, input TokenEstimateInput) (int, error) {
    chars := len(input.SystemPrompt) + len(input.InterviewMaterial) + len(input.UserQuery)
    
    for _, msg := range input.History {
        chars += len(msg.Content)
    }
    
    for _, schema := range input.ToolSchemas {
        chars += len(schema)
    }
    
    if input.Summary != nil {
        chars += len(input.Summary.Goal)
        for _, fact := range input.Summary.ConfirmedFacts {
            chars += len(fact)
        }
        for _, decision := range input.Summary.Decisions {
            chars += len(decision)
        }
    }
    
    tokens := int(math.Ceil(float64(chars) / e.charsPerToken))
    return tokens, nil
}
```

### 3.3 预算验证时机

Manager.Prepare 必须在以下时机验证预算：

1. **初次准备**：BeginTurn 后首次模型调用前
2. **工具循环**：每次工具结果返回后、下一次模型调用前
3. **压缩后**：摘要生成并应用后重新估算

验证失败必须返回明确错误，不得静默截断或发起超限调用。

## 4. 合法边界选择

### 4.1 边界规则

压缩前缀必须满足：

- 以完整用户轮次为边界（user + assistant + 所有 toolResults）
- assistant 的所有 toolCall 必须有对应的 toolResult
- 保护当前用户请求（BaseEntryID 之后的内容）
- 保护任何 pending tool group

### 4.2 BoundarySelector 接口

```go
type BoundarySelector interface {
    SelectPrefix(ctx context.Context, input BoundaryInput) (BoundaryResult, error)
}

type BoundaryInput struct {
    Snapshot          conversation.ProjectionSnapshot
    BaseEntryID       conversation.EntryID
    AvailableTokens   int
    CurrentEstimate   int
}

type BoundaryResult struct {
    PrefixEntries     []conversation.ContextMessage // 可压缩前缀
    KeptEntries       []conversation.ContextMessage // 必须保留的后缀
    FirstKeptEntryID  conversation.EntryID
    PrefixTokens      int
    KeptTokens        int
    NeedsCompaction   bool
}
```

### 4.3 选择算法

```go
func (s *DefaultBoundarySelector) SelectPrefix(ctx context.Context, input BoundaryInput) (BoundaryResult, error) {
    if input.CurrentEstimate <= input.AvailableTokens {
        return BoundaryResult{NeedsCompaction: false}, nil
    }
    
    // 从头开始累积，找到最大合法前缀
    var prefix, kept []conversation.ContextMessage
    var prefixTokens, keptTokens int
    var firstKept conversation.EntryID
    
    groups := groupByTurn(input.Snapshot.Messages)
    
    for i, group := range groups {
        groupTokens := s.estimator.EstimateGroup(group)
        
        // 检查是否完整闭合
        if !isComplete(group) {
            // 后续所有内容必须保留
            kept = append(kept, flattenGroups(groups[i:])...)
            keptTokens = input.CurrentEstimate - prefixTokens
            if i > 0 {
                firstKept = groups[i][0].Sources[0].EntryID
            }
            break
        }
        
        // 检查是否在 BaseEntryID 之前
        if isAfterBase(group, input.BaseEntryID) {
            kept = append(kept, flattenGroups(groups[i:])...)
            keptTokens = input.CurrentEstimate - prefixTokens
            firstKept = group[0].Sources[0].EntryID
            break
        }
        
        // 尝试将该组加入前缀
        if keptTokens+groupTokens <= input.AvailableTokens {
            // 后缀仍在预算内，可以压缩该组
            prefix = append(prefix, group...)
            prefixTokens += groupTokens
        } else {
            // 后缀将超预算，该组必须保留
            kept = append(kept, flattenGroups(groups[i:])...)
            keptTokens = input.CurrentEstimate - prefixTokens
            firstKept = group[0].Sources[0].EntryID
            break
        }
    }
    
    if len(prefix) == 0 {
        return BoundaryResult{}, errors.New("no compactable prefix: indivisible content exceeds budget")
    }
    
    return BoundaryResult{
        PrefixEntries:    prefix,
        KeptEntries:      kept,
        FirstKeptEntryID: firstKept,
        PrefixTokens:     prefixTokens,
        KeptTokens:       keptTokens,
        NeedsCompaction:  true,
    }, nil
}
```

## 5. 摘要生成

### 5.1 ContextSummarizer 实现

已有接口定义，Phase 4 实现基于 Eino 模型调用：

```go
type EinoContextSummarizer struct {
    modelProvider ModelProvider
    promptTemplate string
}

func (s *EinoContextSummarizer) Summarize(ctx context.Context, input SummaryPrompt) (ContextSummary, error) {
    // 构造摘要 prompt
    prompt := s.buildPrompt(input)
    
    // 调用模型
    response, err := s.modelProvider.Generate(ctx, ModelRequest{
        Messages: []ModelMessage{
            {Role: "user", Content: prompt},
        },
        Temperature: 0.0,
        MaxTokens:   input.BudgetTokens,
    })
    if err != nil {
        return ContextSummary{}, fmt.Errorf("summarize context: %w", err)
    }
    
    // 解析结构化输出
    var summary ContextSummary
    if err := json.Unmarshal([]byte(response.Content), &summary); err != nil {
        return ContextSummary{}, fmt.Errorf("parse summary: %w", err)
    }
    
    // 验证 schema
    if err := validateSummary(summary); err != nil {
        return ContextSummary{}, fmt.Errorf("invalid summary: %w", err)
    }
    
    summary.Version = 1 // 当前 schema 版本
    return summary, nil
}

func (s *EinoContextSummarizer) buildPrompt(input SummaryPrompt) string {
    var buf strings.Builder
    buf.WriteString("请总结以下对话历史，提取关键信息：\n\n")
    
    if input.ExistingSummary != nil {
        buf.WriteString("现有摘要：\n")
        buf.WriteString(formatSummary(input.ExistingSummary.Summary))
        buf.WriteString("\n新增对话：\n")
    }
    
    for _, msg := range input.History {
        buf.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
    }
    
    buf.WriteString("\n请以 JSON 格式输出，包含以下字段：\n")
    buf.WriteString("- goal: 用户的总体目标\n")
    buf.WriteString("- confirmedFacts: 已确认的事实（字符串数组）\n")
    buf.WriteString("- decisions: 已做出的决策（字符串数组）\n")
    buf.WriteString("- constraints: 约束条件（字符串数组）\n")
    buf.WriteString("- openQuestions: 未解决的问题（字符串数组）\n")
    
    return buf.String()
}
```

### 5.2 SummaryCoordinator 实现

```go
type DefaultSummaryCoordinator struct {
    summarizer ContextSummarizer
    store      SummaryStore
}

func (c *DefaultSummaryCoordinator) Prepare(ctx context.Context, input SummaryInput) (SummaryResult, error) {
    // 检查是否可以复用现有摘要
    if input.ExistingSummary != nil {
        if canReuse(input.ExistingSummary, input.OmittedHistory, input.BudgetTokens) {
            return SummaryResult{
                Summary: input.ExistingSummary,
                Used:    true,
                Updated: false,
            }, nil
        }
    }
    
    // 需要生成新摘要
    summary, err := c.summarizer.Summarize(ctx, SummaryPrompt{
        ConversationID:  input.ConversationID,
        ExistingSummary: input.ExistingSummary,
        History:         input.OmittedHistory,
        BudgetTokens:    input.BudgetTokens,
    })
    if err != nil {
        return SummaryResult{}, fmt.Errorf("generate summary: %w", err)
    }
    
    // 验证摘要在预算内
    estimatedTokens := estimateSummaryTokens(summary)
    if estimatedTokens > input.BudgetTokens {
        return SummaryResult{}, fmt.Errorf("summary exceeds budget: %d > %d", estimatedTokens, input.BudgetTokens)
    }
    
    return SummaryResult{
        Summary: &ContextSummaryRecord{
            ConversationID: input.ConversationID,
            SchemaVersion:  1,
            Summary:        summary,
            UpdatedAt:      time.Now(),
        },
        Used:    true,
        Updated: true,
    }, nil
}
```

## 6. Compaction Entry 持久化

### 6.1 Domain 模型

```go
// domain/conversation/compaction.go

type CompactionEntry struct {
    ID                conversation.EntryID
    ConversationID    string
    ParentID          conversation.EntryID
    Kind              string // "compaction"
    PayloadVersion    int    // 1
    FirstKeptEntryID  conversation.EntryID
    TokensBefore      int
    Summary           CompactionSummary
    CreatedAt         time.Time
}

type CompactionSummary struct {
    Version        int      `json:"version"`
    Goal           string   `json:"goal"`
    ConfirmedFacts []string `json:"confirmedFacts"`
    Decisions      []string `json:"decisions"`
    Constraints    []string `json:"constraints"`
    OpenQuestions  []string `json:"openQuestions"`
}

type CompactionWriter interface {
    AppendCompaction(ctx context.Context, input CompactionInput) (conversation.EntryID, error)
}

type CompactionInput struct {
    ConversationID   string
    ParentID         conversation.EntryID
    FirstKeptEntryID conversation.EntryID
    TokensBefore     int
    Summary          CompactionSummary
    ExpectedLeaf     conversation.EntryID
    ExpectedVersion  int
}

type CompactionReader interface {
    ReadLatestCompaction(ctx context.Context, query CompactionQuery) (*CompactionEntry, error)
}

type CompactionQuery struct {
    ConversationID string
    OnPath         []conversation.EntryID // active path，用于验证
}
```

### 6.2 SQLite 实现

```go
// sqlite/compaction_store.go

func (r *AgentRunRepository) AppendCompaction(ctx context.Context, input conversation.CompactionInput) (conversation.EntryID, error) {
    tx, err := r.db.Begin()
    if err != nil {
        return "", err
    }
    defer tx.Rollback()
    
    // 验证 firstKeptEntryID 在 parent 的祖先路径上
    valid, err := r.tree.isAncestor(tx, input.ConversationID, input.FirstKeptEntryID, input.ParentID)
    if err != nil {
        return "", err
    }
    if !valid {
        return "", conversation.ErrInvalidCompactionBoundary
    }
    
    // 序列化 summary
    summaryJSON, err := json.Marshal(input.Summary)
    if err != nil {
        return "", fmt.Errorf("marshal summary: %w", err)
    }
    
    // 构造 compaction payload
    payload := map[string]interface{}{
        "kind":              "compaction",
        "payloadVersion":    1,
        "firstKeptEntryID":  input.FirstKeptEntryID,
        "tokensBefore":      input.TokensBefore,
        "summary":           input.Summary,
    }
    payloadJSON, err := json.Marshal(payload)
    if err != nil {
        return "", err
    }
    
    // 追加 Entry（内部执行 CAS）
    entryID, err := r.tree.appendTx(tx, input.ConversationID, input.ParentID, payloadJSON, input.ExpectedLeaf, input.ExpectedVersion)
    if err != nil {
        return "", err
    }
    
    if err := tx.Commit(); err != nil {
        return "", err
    }
    
    return entryID, nil
}

func (r *AgentRunRepository) ReadLatestCompaction(ctx context.Context, query conversation.CompactionQuery) (*conversation.CompactionEntry, error) {
    // 从 leaf 向 root 遍历，找到第一个 compaction entry
    for _, entryID := range reverse(query.OnPath) {
        entry, err := r.tree.Get(ctx, entryID)
        if err != nil {
            return nil, err
        }
        
        var payload map[string]interface{}
        if err := json.Unmarshal(entry.Payload, &payload); err != nil {
            continue
        }
        
        if payload["kind"] == "compaction" {
            // 验证 firstKeptEntryID 仍在当前路径上
            firstKept := conversation.EntryID(payload["firstKeptEntryID"].(string))
            if !contains(query.OnPath, firstKept) {
                return nil, conversation.ErrInvalidCompactionBoundary
            }
            
            var summary conversation.CompactionSummary
            summaryBytes, _ := json.Marshal(payload["summary"])
            if err := json.Unmarshal(summaryBytes, &summary); err != nil {
                return nil, err
            }
            
            return &conversation.CompactionEntry{
                ID:               entryID,
                ConversationID:   entry.ConversationID,
                ParentID:         entry.ParentID,
                FirstKeptEntryID: firstKept,
                TokensBefore:     int(payload["tokensBefore"].(float64)),
                Summary:          summary,
                CreatedAt:        entry.CreatedAt,
            }, nil
        }
    }
    
    return nil, nil // 无 compaction
}
```

### 6.3 Migration

新增 `0010_compaction_entries.sql`（实际上不需要新表，compaction 作为 Entry 的一种 kind 存储在 `conversation_entry_nodes` 中）：

```sql
-- 0010_compaction_entries.sql
-- Compaction entries are stored in conversation_entry_nodes with kind='compaction'
-- No new tables needed

-- Add index for efficient compaction lookup on active paths
CREATE INDEX IF NOT EXISTS idx_entry_nodes_conversation_kind
ON conversation_entry_nodes(conversation_id, kind)
WHERE kind = 'compaction';
```

## 7. Projection 与 Compaction 集成

### 7.1 Manager.Prepare 完整流程

```go
func (m *Manager) Prepare(ctx context.Context, input chat.ContextInput) (chat.PreparedContext, error) {
    // 1. 获取预算计划
    plan, err := m.budgetPolicy.Plan(ctx, input)
    if err != nil {
        return chat.PreparedContext{}, err
    }
    
    // 2. 读取 projection
    snapshot, err := m.projectionReader.ReadProjection(ctx, conversation.ProjectionQuery{
        ConversationID: input.Conversation.ID,
        RunID:          string(input.RunID),
        UserMessageID:  input.UserMessage.ID,
    })
    if err != nil {
        return chat.PreparedContext{}, err
    }
    
    // 3. 估算当前 token
    currentTokens, err := m.tokenEstimator.EstimateTokens(ctx, TokenEstimateInput{
        SystemPrompt:      input.SystemPrompt,
        History:           snapshot.Messages,
        InterviewMaterial: input.InterviewContext,
        UserQuery:         input.UserMessage.Content,
        ToolSchemas:       input.ToolSchemas,
    })
    if err != nil {
        return chat.PreparedContext{}, err
    }
    
    // 4. 检查是否需要压缩
    if currentTokens <= plan.HistoryTokens {
        // 在预算内，直接返回
        return chat.PreparedContext{
            ModelHistory:    convertToModelHistory(snapshot.Messages),
            InterviewContext: input.InterviewContext,
            Summary:         nil,
        }, nil
    }
    
    // 5. 选择压缩边界
    boundary, err := m.boundarySelector.SelectPrefix(ctx, BoundaryInput{
        Snapshot:        snapshot,
        BaseEntryID:     snapshot.Head.BaseEntryID,
        AvailableTokens: plan.HistoryTokens,
        CurrentEstimate: currentTokens,
    })
    if err != nil {
        return chat.PreparedContext{}, err
    }
    
    if !boundary.NeedsCompaction {
        return chat.PreparedContext{
            ModelHistory:     convertToModelHistory(snapshot.Messages),
            InterviewContext: input.InterviewContext,
        }, nil
    }
    
    // 6. 检查是否有可复用的 compaction
    existingCompaction, err := m.compactionReader.ReadLatestCompaction(ctx, conversation.CompactionQuery{
        ConversationID: input.Conversation.ID,
        OnPath:         extractEntryIDs(snapshot.Messages),
    })
    if err != nil && !errors.Is(err, conversation.ErrNotFound) {
        return chat.PreparedContext{}, err
    }
    
    // 7. 生成或复用摘要
    summaryResult, err := m.summaryCoordinator.Prepare(ctx, SummaryInput{
        ConversationID:  input.Conversation.ID,
        OmittedHistory:  convertToMessages(boundary.PrefixEntries),
        ExistingSummary: convertCompactionToSummary(existingCompaction),
        BudgetTokens:    plan.SummaryTokens,
    })
    if err != nil {
        return chat.PreparedContext{}, err
    }
    
    // 8. 如果生成了新摘要，需要持久化为 compaction entry
    if summaryResult.Updated {
        _, err := m.compactionWriter.AppendCompaction(ctx, conversation.CompactionInput{
            ConversationID:   input.Conversation.ID,
            ParentID:         boundary.PrefixEntries[len(boundary.PrefixEntries)-1].Sources[0].EntryID,
            FirstKeptEntryID: boundary.FirstKeptEntryID,
            TokensBefore:     boundary.PrefixTokens,
            Summary:          convertToCompactionSummary(summaryResult.Summary.Summary),
            ExpectedLeaf:     snapshot.Head.LeafID,
            ExpectedVersion:  snapshot.Head.Version,
        })
        if err != nil {
            // CAS 失败不是致命错误，可以继续使用摘要（只是没持久化）
            if !errors.Is(err, conversation.ErrVersionConflict) {
                return chat.PreparedContext{}, err
            }
        }
    }
    
    // 9. 返回带摘要的上下文
    return chat.PreparedContext{
        ModelHistory:     convertToModelHistory(boundary.KeptEntries),
        InterviewContext: input.InterviewContext,
        Summary:          summaryResult.Summary,
    }, nil
}
```

## 8. Legacy 摘要迁移

### 8.1 现有摘要表

现有 `conversation_context_summaries` 表使用 `covered_sequence` 标记覆盖范围，不能直接映射到 Entry Tree。

### 8.2 迁移策略

两种处理方式：

1. **失效重建**：将旧摘要标记为 invalid，在预算允许时重新生成
2. **显式映射**：找到 `covered_sequence` 对应的最后一个 Entry，创建等效的 compaction entry

Phase 4 采用**失效重建**策略，更简单且更安全：

```go
func (c *DefaultSummaryCoordinator) Prepare(ctx context.Context, input SummaryInput) (SummaryResult, error) {
    // 检查现有摘要
    if input.ExistingSummary != nil {
        // 验证是否是基于 Entry Tree 的新摘要
        if input.ExistingSummary.CompactionEntryID == "" {
            // 旧摘要，标记为 fallback 并重建
            return c.rebuild(ctx, input)
        }
        
        // 新摘要，检查是否可复用
        if canReuse(input.ExistingSummary, input.OmittedHistory, input.BudgetTokens) {
            return SummaryResult{
                Summary: input.ExistingSummary,
                Used:    true,
                Updated: false,
            }, nil
        }
    }
    
    // 生成新摘要
    return c.generate(ctx, input)
}
```

## 9. 实施顺序与切片

Phase 4 建议分 3 个切片实施：

### 切片 1：预算强制与边界选择

- [ ] 实现 `EnforcedBudgetPolicy`
- [ ] 实现 `CharBasedTokenEstimator`
- [ ] 实现 `DefaultBoundarySelector`
- [ ] Manager 集成预算验证
- [ ] 测试：预算超限拒绝、完整组边界、保护区检测

### 切片 2：摘要生成与 Compaction Entry

- [ ] 实现 `EinoContextSummarizer`
- [ ] 实现 `DefaultSummaryCoordinator`
- [ ] Domain `CompactionEntry` 模型
- [ ] SQLite `CompactionWriter` / `CompactionReader`
- [ ] Migration `0010_compaction_entries.sql`
- [ ] 测试：摘要生成、schema 验证、CAS 冲突、路径校验

### 切片 3：集成与复用

- [ ] Manager.Prepare 完整流程集成
- [ ] 重启后复用逻辑
- [ ] Legacy 摘要失效与重建
- [ ] 测试：端到端压缩、重启复用、混合历史、并发 CAS

## 10. 测试策略

### 10.1 单元测试

- `budget_test.go`：预算计划、窗口校验、配置验证
- `boundary_test.go`：边界选择、完整组检测、保护区、超大组
- `summarizer_test.go`：摘要生成、schema 解析、预算验证
- `compaction_store_test.go`：Entry 追加、CAS、路径校验、读取

### 10.2 集成测试

- `manager_compaction_test.go`：
  - 预算内无压缩
  - 预算外生成摘要
  - 重启复用 compaction
  - 并发 CAS 失败处理
  - Legacy 摘要失效

- `context_projection_compaction_test.go`：
  - Projection + Compaction 端到端
  - 摘要 + 保留后缀组合
  - 工具组跨边界场景

### 10.3 回归测试

- Phase 3 所有 projection 测试必须继续通过
- 现有无摘要路径必须正常工作
- HTTP/SSE 行为不变

## 11. 风险与缓解

| Risk | Mitigation |
|---|---|
| 摘要生成失败阻塞对话 | 摘要失败时返回明确容量错误，不静默截断 |
| CAS 冲突导致重复生成 | CAS 失败时可继续使用摘要（未持久化），不阻塞当前请求 |
| 边界选择错误拆分工具组 | 完整组校验、pending 检测、保护区验证 |
| 旧摘要混淆树边界 | 强制检查 CompactionEntryID，旧摘要失效重建 |
| Token 估算偏差 | 保留安全余量、实际模型调用仍有 SDK 层保护 |
| 摘要质量问题 | 结构化 schema 强制、验证器、可观测性日志 |

## 12. 可观测性

- Manager 记录预算决策、压缩触发原因、边界选择结果
- 摘要生成时记录输入 token、输出 token、耗时
- CAS 冲突时记录冲突详情
- 复用时记录 compaction ID 和来源

## 13. 性能目标

- 边界选择：<10ms（纯内存计算）
- 摘要生成：取决于模型，通常 1-3s
- Compaction Entry 提交：<50ms（单次事务）
- 重启复用读取：<20ms（索引查询）

## 14. 未来扩展

Phase 4 之后可选扩展：

- **多级压缩**：对已压缩的摘要再次压缩
- **用户可见摘要**：前端展示当前对话摘要
- **摘要编辑**：用户修正摘要内容
- **自适应边界**：根据模型窗口动态调整保留长度
- **摘要质量评估**：自动检测摘要信息丢失

## 15. 验收标准

Phase 4 完成标准：

- ✅ 所有单元测试通过
- ✅ 所有集成测试通过
- ✅ Phase 3 回归测试通过
- ✅ `go test -count=1 ./...` 通过
- ✅ `go vet ./...` 通过
- ✅ 真实模型端到端验证（生成摘要并继续对话）
- ✅ 重启后成功复用 compaction
- ✅ 并发场景下 CAS 正确处理
- ✅ Legacy 摘要正确失效和重建
- ✅ 文档更新（本技术设计 + tasks.md）

Phase 4 **不**声称完成：

- ❌ 多级压缩
- ❌ 用户可见摘要 UI
- ❌ 摘要编辑功能
- ❌ artifact（独立可选能力）
- ❌ 产品 Phase 3 长期记忆

---

**状态：待用户授权后开始实施**
