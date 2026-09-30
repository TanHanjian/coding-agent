# Design

## 1. 目标架构

```text
Conversation Entry Tree
        |
        v
Context Projection
        |
        v
Provider-neutral AgentMessage
        |
        v
Eino schema.Message
```

`ConversationEntry` 是不可变上下文事实和会话状态 entry 的统一树节点；`AgentRun` 是一次外部用户请求的执行生命周期；`RunEvent` 是运行期事件和受限恢复摘要；`ContextProjection` 是唯一决定模型实际看到什么的边界。

### 已确认的设计决策

- 使用 Pi 式 `parentId + activeLeafId`，不引入具名分支记录。
- 同一个 Conversation 同时只允许一个活跃 Run；后到请求返回明确冲突，不交叉追加同一条 active path。
- 工具消息采用独立 `assistant` tool-call message 和独立 `toolResult` entry；多个并发结果按 assistant 声明的 tool-call 顺序追加。
- 工具数据按工具级白名单生成安全投影；未声明字段不落树，超大结果只保留有界预览和 artifact 引用。
- 压缩摘要作为树上的 `compaction` entry 持久化；原始 entry 不删除、不覆盖。
- 旧 `conversation.Message` 继续作为兼容的用户可见 read model，采用双写和逐步读取切换。

## 2. Capability Dependency Map

| Module id | Responsibility | Depends on |
|---|---|---|
| `entry-tree` | 树节点、active leaf、append-only 约束、分支、工具安全投影、SQLite 持久化 | — |
| `agent-run` | 请求幂等、运行状态、取消、流式事件、重启收敛、单会话串行 | `entry-tree` |
| `context-projection` | active path、投影规则、消息协议校验、provider-neutral 到 Eino 转换 | `entry-tree` |
| `context-compaction` | 预算、逻辑组边界、摘要 entry、CAS、摘要复用 | `entry-tree`, `context-projection` |

实现顺序：`entry-tree` → `agent-run` 与 `context-projection` 并行 → `context-compaction` → 旧路径清理。

## 3. Domain Contracts

### 3.1 ConversationEntry

建议领域结构：

```go
type ConversationEntry struct {
    ID             EntryID
    ConversationID string
    ParentID       *EntryID
    Type           EntryType
    PayloadVersion int
    Payload        EntryPayload
    CreatedAt      time.Time
}
```

Entry 类型至少包括：`message`、`compaction`、`branch_summary`、`context_edit`、`model_change`、`thinking_level_change`、`usage`、`custom`。

`message` payload 的 provider-neutral role 至少包括：`system`、`user`、`assistant`、`toolResult`。assistant content 支持 text、thinking、toolCall 和 image；tool result 通过 `toolCallId` 关联到 assistant tool call。

树不依赖全局线性 sequence 决定上下文。可保留 append index 或兼容展示 sequence，但它们只能用于审计、排序或 read model，不能替代 `ParentID`。

### 3.2 Tree Operations

Entry repository 至少提供以下语义：

- 读取当前 active leaf 和从 root 到 leaf 的路径；
- 以 `expectedParentID` CAS 追加 child entry；
- 将 active leaf 移动到已有 entry 以创建分支；
- 读取 entry、子节点和指定 leaf 的路径；
- 追加 compaction/context edit 等非普通 message entry。

所有追加操作必须保持 append-only。旧 entry 不允许原地修改 payload、parent 或时间戳；状态变化应通过新 entry 或独立运行状态完成。

### 3.3 Tool Projection

工具定义提供版本化 `ToolProjectionPolicy`，声明可持久化字段、最大预览长度、敏感字段和 artifact 能力。持久化投影可以包含工具名、调用 ID、必要的有界参数摘要、有界结果摘要、错误标记、状态和 artifact metadata，但不得包含未声明字段、凭据、完整原始 payload 或内部错误堆栈。

artifact 由独立受限存储管理，使用服务端生成的不透明 ID，绑定 conversation/run/tool call，支持单次读取额度、TTL、会话隔离、原子发布和清理。artifact 不成为永久消息事实；过期读取必须返回明确的 expired 状态。

### 3.4 AgentRun

```go
type AgentRun struct {
    ID              RunID
    ConversationID  string
    BaseEntryID     *EntryID
    UserEntryID     EntryID
    LastEntryID     EntryID
    FinalEntryID    *EntryID
    ClientMessageID string
    Status          RunStatus
    ErrorCode       string
    ErrorMessage    string
    CreatedAt       time.Time
    FinishedAt      *time.Time
}
```

`Run` 只保存执行身份、状态、入口/出口 entry 引用和脱敏错误分类；不保存完整模型输入/输出或完整工具正文。状态为 `pending`、`running`、`waiting_tool`、`completed`、`failed`、`cancelled`。

`RunEvent` 记录 `model_call_started`、文本 delta、tool call、tool started/completed、terminal 等事件，内容遵循现有 `generation_events` 和 `MemoryGenerationHub` 的脱敏边界。事件不参与 Context Projection。

服务重启前置收敛遗留 active Run 为 `failed/generation_interrupted`，不恢复模型执行。相同 client id 返回既有 Run/结果；新 client id 创建新 Run。

### 3.5 Context Projection

投影沿 active leaf 回溯并反转路径；如果路径包含最新 compaction，则以 summary/checkpoint 替代被覆盖前缀，保留 `firstKeptEntryId` 后的合法消息和 compaction 之后的 entry。context edit 只改变当前分支的模型内容，不改原始 entry。

投影必须保留来源映射：

```go
type ProjectedEntry struct {
    SourceEntryID EntryID
    Messages      []AgentMessage
}

type ContextProjection struct {
    LeafID  EntryID
    Entries []ProjectedEntry
    Messages []AgentMessage
}
```

在转换前校验 assistant tool call 和 tool result 的完整配对、角色顺序、重复/孤立/缺失结果。转换器只负责 `AgentMessage -> Eino schema.Message`，不能吞掉 call ID、tool name、错误标记或未支持的协议字段。

### 3.6 Compaction

压缩选择器以完整用户轮次和完整工具交互组为逻辑单位。候选边界由程序决定，LLM 只总结已选择内容。当前请求和未完成工具组属于保护区；超大工具结果先按安全投影/artifact 策略处理，不能通过拆开 tool-call/tool-result 满足预算。

`compaction` entry 至少保存 summary schema/version、`firstKeptEntryId`、压缩前估算 token、摘要模型 usage（如允许）和必要的安全 metadata。摘要生成、结构校验、预算校验或 active leaf CAS 失败时，不追加 entry、不推进边界、不修改原始历史。

最终预算满足：

```text
输入估算 + 输出预留 + 安全余量 <= 模型上下文窗口
```

### 3.7 兼容迁移

当前 `ChatTurnRepository.BeginTurn` 会在线性 `messages` 中预建 streaming assistant，`AppendAssistantText` 原地追加正文；当前 `ContextManager` 和 `toSchemaHistory` 只支持 user/assistant 文本。迁移必须分阶段完成：

1. 创建 Entry Tree domain contract 和 repository，不改变旧聊天行为；
2. 将现有 user/assistant 事实双写为 Entry，并保留旧 `messages` read model；
3. 将 `AgentRun` 接到现有 client id、取消、重启收敛和 generation event 边界；
4. 以 Projection 替换旧线性 history，并加入 provider-neutral tool messages；
5. 接入安全工具投影和 compaction；
6. 在回归和迁移验收通过后再移除旧转换路径。

迁移不得把旧 `sequence` 当作 parent，也不得把已有 transient/full tool payload 写回旧 `messages`。新增表采用兼容 migration；旧数据回填失败时保留旧 read model 可用并暴露可诊断的迁移状态。

## 4. Persistence Sketch

建议新增以下持久化边界，最终字段以实现前 schema review 为准：

- `conversation_entry_nodes`：entry id、conversation id、parent id、type、payload version、safe payload、created at；建立 parent/path 查询索引。
- `conversation_heads` 或在 `conversations` 中增加 active leaf/version：CAS 更新 active leaf，保留当前会话唯一 head。
- `agent_runs`：run identity、client id、base/user/last/final entry references、status、脱敏错误和时间。
- `agent_run_events`：run/event id、event sequence、脱敏 kind、step id、safe summary、created at。
- `conversation_artifacts`：不透明 artifact id、conversation/run/tool call 归属、大小、content type、expiry、状态和受限物理引用。

现有 `messages`、`generation_events`、`conversation_context_summaries` 不直接删除；迁移时明确 source of truth、双写失败策略和回填水位。

## 5. Risks and Mitigations

| Risk | Mitigation |
|---|---|
| 双写不一致 | 事务内写入或明确 outbox/retry 状态；旧 read model 仍可用；禁止静默标记成功 |
| active leaf 并发追加 | 会话级串行锁加 expected-parent CAS；冲突后重新投影 |
| 安全投影泄露 | 工具级白名单、字段过滤、长度上限、凭据阻断测试；默认拒绝未声明字段 |
| 工具协议被压缩破坏 | 按逻辑组选择，投影前后做配对校验；不可拆当前工具组 |
| 摘要回退覆盖新状态 | active leaf/version CAS；候选摘要失败不发布 |
| 旧历史无法恢复工具协议 | 旧记录只映射为 text；新 Entry 从迁移点开始提供完整安全结构 |
| Eino 运行时能力不足 | 保持 converter 独立；对不支持的结构返回明确错误，不静默丢失字段 |

## 6. Verification Gates

- 规范 review：确认四个模块、单 active leaf、单会话单 Run、安全工具白名单和 compaction entry。
- Entry Tree gate：树路径、分支、CAS、append-only 和安全投影测试通过。
- Run/Projection gate：幂等、冲突、取消、重启收敛、工具消息转换和事件脱敏测试通过。
- Compaction gate：合法边界、摘要原子性、CAS、预算复检和重启复用测试通过。
- Migration gate：旧历史、双写失败、回填、SSE/HTTP 兼容和全量 Go 测试通过。
