# Phase 2 技术设计：AgentRun 生命周期与聊天接入

> 状态：方案范围已确认；经独立开工确认，**切片 1 的 Run 领域模型与聊天契约已实现并通过测试**。其余持久化、运行接线、恢复和生产装配尚未实现，Phase 2 功能未交付。
> 所属变更：[refactor-conversation-message-tree](../openspec/changes/refactor-conversation-message-tree/design.md)。
> 前置设计：[Phase 1 Entry Tree](phase1-entry-tree-technical-design.md)。
> 最初方案轮只落实文档；后续实现按切片单独批准。切片 1 不修改前端、migration 或生产装配。

## 1. 已确认决策与范围

用户已确认以下三点：

1. Phase 2 包含 AgentRun 接入现有聊天流程，以及完整 assistant/toolResult 消息的安全写树；模型历史暂时仍来自旧路径。
2. 工具组未闭合时发生失败、取消或重启中断：保留不可变失败分支，在终态事务中将 active head 移回最近闭合点。
3. HTTP/SSE 继续使用 assistantMessageID，不要求前端切换为 Run ID。

本文的 Phase 1/2/3 是**实施阶段**，不是需求标题 `[MVP]`、`[Phase 2]`、`[Phase 3]` 中的**产品交付阶段**：

| 实施阶段 | 交付内容 | 产品阶段 |
| --- | --- | --- |
| Phase 1 | Entry Tree、head/version CAS、安全投影基础 | MVP |
| Phase 2（本文） | Run 生命周期、关键原子写入、运行接线、恢复 | MVP |
| Phase 3 | Context Projection、明确的模型消息转换与历史切换 | MVP |
| Phase 4 | compaction、预算与摘要协调 | 产品 Phase 2 |
| 后续可选工作 | artifact、受限读回、TTL，以及独立迁移验收 | 按对应产品规范，不属于本文 |

### 1.1 本阶段交付

- 独立 Run 领域模型和六种生命周期状态。
- 请求幂等、单会话单 active Run。
- 原子创建 user Entry、旧 Message 对、pending Run。
- 工具执行前提交完整 assistant 调用消息；下一次模型调用前提交完整安全工具结果。
- 最终 assistant Entry、Run 终态、旧 assistant 终态一致提交。
- 启动/注册失败补偿，取消与完成竞争，迟到写入拦截。
- 排他运行权下的启动恢复。
- 旧请求重试和 HTTP/SSE 兼容。

### 1.2 不交付

不实现 Context Projection、Eino 历史转换替换、compaction、旧历史全量回填、Draft、artifact 存储、具名 Branch、通用 RunEvent 表、SSE 事件重放、revision 重同步、模型/工具执行恢复或自动重放。

不持久化 thinking、system prompt 原文、provider 原始响应、未声明工具字段或错误堆栈。

**生产装配不开放用户分支切换。** 否则树 head 已切换，但旧线性 history 仍包含其他分支。失败时移回闭合点是 Run 的受控内部收尾，不是前端分支功能。

## 2. 问题与现有实现

Phase 1 已提供不可变消息节点、父链、head 和事务 CAS，但还没有“本轮由谁写入”的执行身份。

| 当前位置 | 现有职责 | Phase 2 调整 |
| --- | --- | --- |
| `application/chat/service.go` | BeginTurn、后台 Stream、统一 FinishAssistant | 加入 Run 启动门禁、补偿和正式响应收尾 |
| `application/chat/types.go` | Request、Begin/Finish 输入输出 | 加 Run 引用、Recorder 和最终完整消息 |
| `application/chat/stream.go` | 64字节/80ms 批量可见正文，先提交再广播 | 保留，写入时校验 Run active |
| `application/chat/generation.go` | Hub 快照/增量与 Registry 取消句柄 | 保留外部 assistant ID；修正服务关闭取消传播 |
| `repository/sqlite/chat_turn_store.go` | client ID 幂等、旧消息对、线性 history | 原子整合 Run 和 Entry |
| `repository/sqlite/entry_tree_repository.go` | 对外 Append/MoveHead 自行开启事务 | 提取包内事务核心，避免嵌套事务 |
| `agent/eino/graph_events.go` | 临时安全展示事件，部分通知错误被忽略 | 继续用于展示，不承担必须成功的事实写入 |
| `agent/interview/builder.go` | 完整工具调用/结果在 Graph 私有状态中 | 接入可传播错误的正式消息控制路径 |
| `cmd/server/main.go` | 当前没有 Run 中断恢复门禁 | 排他运行权、恢复成功后才接受请求 |

关键缺口：

- Registry 注册失败可能留下无人执行的 streaming Message。
- 当前消息终态不能表达完整多轮 Agent Loop 的执行身份。
- 没有 Run 的 Entry 引用、工具等待状态或启动恢复。
- `MaxOpenConns(1)` 下不能在外层事务里调用公共 EntryTree.Append/GetHead。
- SSE 工具摘要不是完整消息事实。

## 3. 架构与职责

```text
HTTP（保持现有请求、订阅、取消地址）
        │
        ▼
Chat Service
  ├─ BeginTurn / MarkRunning / 取消控制 / 统一终态
  ├─ 文本 sink ──────────→ 旧 messages → Hub → SSE
  └─ 本 Run 的 Recorder
          ▲                  │
          │完整安全消息         ▼
Eino Executor / Graph     GenerationStore
  ├─ 可见 delta               │
  ├─ 完整 assistant           ▼
  └─ 完整 tool results     一个 SQLite 事务
                             ├─ agent_runs
                             ├─ entry_nodes + heads
                             └─ 旧 messages
```

### 3.1 数据来源

| 数据 | Source of truth | 说明 |
| --- | --- | --- |
| Run 身份、状态与 client ID | agent_runs | Registry 缺失不等于 DB Run 中断 |
| 正式消息与工具安全事实 | Entry Tree | 收敛后只追加，不逐 delta 修改 |
| 当前兼容历史与可见正文 | messages | 本阶段模型仍使用旧 history |
| 原始工具参数/结果 | 本次 Graph 内存 | 执行需要，不持久化，不从摘要反推 |
| 活跃订阅、临时 step 摘要 | MemoryGenerationHub | 进程内快照，不承诺重放 |
| 取消句柄与完成通知 | MemoryGenerationRegistry | 进程内控制，不替代 Run 状态 |

双写期间不宣称一张表能替代所有数据。关键联动写入必须同事务；既有 generation_events 和线性 summary 表保留原状，不扩展为通用 Run 重放系统。

### 3.2 Run 不保存第二套正文

一个 Run 可以产生多条 Entry：

```text
U1 → A1[text, call(c1), call(c2)] → T1(c1) → T2(c2) → A2[最终回答]
     └────────────── 同一个 AgentRun ────────────────────────┘
```

旧 assistant Message 仍是一条连续的用户可见输出，可能含多个模型 step 的前导文字。不能把它的累计 Content 当成单条完整模型响应再写入树。

## 4. Run 领域类型与不变量

建议新建 `backend/internal/domain/agentrun/`。以下是拟议契约，不是已存在符号。

```go
// 需要 context/time/conversation 等对应 import；此处省略 import 样板。
type RunID string

type Status string
const (
    StatusPending     Status = "pending"
    StatusRunning     Status = "running"
    StatusWaitingTool Status = "waiting_tool"
    StatusCompleted   Status = "completed"
    StatusFailed      Status = "failed"
    StatusCancelled   Status = "cancelled"
)

type Run struct {
    ID                 RunID
    ConversationID     string
    ClientMessageID    string
    UserMessageID      string
    AssistantMessageID string

    BaseEntryID       *conversation.EntryID
    BaseHeadVersion   int64
    UserEntryID       conversation.EntryID
    LastEntryID       conversation.EntryID
    LastClosedEntryID conversation.EntryID
    FinalEntryID      *conversation.EntryID
    HeadVersion       int64

    Status     Status
    ErrorCode  string
    CreatedAt  time.Time
    StartedAt  *time.Time
    FinishedAt *time.Time
}
```

字段含义：

- BaseEntryID/BaseHeadVersion：本次提交前的 head 快照；允许 nil/v0。
- UserEntryID：本次提交产生的正式 user 节点；新 Run 必须有值。
- LastEntryID：本 Run 最后成功写入的正式事实；不因失败回退而改成另一个事实。
- LastClosedEntryID：当前 Run 最近闭合的路径末端；初始化为 UserEntryID，完整结果组提交后更新。
- FinalEntryID：成功终态的完整最终 assistant；失败/取消时 nil。
- HeadVersion：Run 最近成功的树操作版本。active 时与 LastEntryID 构成 expected head；失败回退后仅记录最新 head version，不能继续据 LastEntryID 构造可写 token。

### 4.1 必须成立的不变量

1. Run active 时，数据库 head 的 leaf/version 等于 Run.LastEntryID/HeadVersion。
2. Run terminal 后不再追加正文或事实；历史 LastEntryID 可以与 active head 不同。
3. LastClosedEntryID 必须是本 Run 路径中合法闭合点；不能是别的分支/会话节点。
4. pending 创建时 LastEntryID=LastClosedEntryID=UserEntryID，FinalEntryID=nil。
5. completed 必须有正式最终响应和 FinishedAt；最终 Entry、Run、旧 Message 同事务。
6. 失败/取消保留已提交事实和已持久化可见正文，不补造工具结果，不保存未完成模型响应。
7. 只保存受控 ErrorCode。旧 Message.ErrorMessage 也只能来自代码到安全文案的映射。

不增加 Run revision、checkpoint、消息 ordinal 表或单独 Draft。本阶段的并发控制采用 active Run 约束、状态条件和现有 head CAS。

### 4.2 状态机

```text
pending ──启动成功──→ running
                       │
             assistant 调用消息提交
                       ▼
                  waiting_tool
                       │
              完整结果组提交
                       └────────→ running

running ──完整最终响应提交──→ completed
pending/running/waiting_tool ──失败或中断──→ failed
pending/running/waiting_tool ──取消收尾──→ cancelled
```

没有任意 `SetStatus` 入口。MarkRunning、提交调用、提交结果、完成/失败收尾各自定义合法状态。领域纯函数提供 IsActive/IsTerminal/ValidateTransition。

## 5. Application Interface 与运行对象

### 5.1 统一 GenerationStore

将现有 TurnStore 演进为主要写入 Interface，原方法名称保留。Store 跨 Run、Entry、旧 Message 三种数据，放在 application/chat；agentrun domain 不依赖 UI read model。

```go
type GenerationStore interface {
    BeginTurn(context.Context, BeginTurnInput) (BeginTurnResult, error)
    MarkRunning(context.Context, agentrun.RunID) (agentrun.Run, error)
    AppendAssistantText(context.Context, string, string) error
    CommitRunMessages(context.Context, CommitRunMessagesInput) (CommitRunMessagesResult, error)
    FinishAssistant(context.Context, FinishAssistantInput) (conversation.Message, error)
    GetMessage(context.Context, string) (conversation.Message, error)
}

type CommitRunMessagesInput struct {
    RunID        agentrun.RunID
    ExpectedHead conversation.HeadToken
    Messages     []conversation.EntryMessage
}

type CommitRunMessagesResult struct {
    Run     agentrun.Run
    Entries []conversation.ConversationEntry
    Head    conversation.EntryHead
}
```

CommitRunMessages 只接受一条完整 assistant 工具调用消息，或者当前调用组的全部独立 toolResult；不是任意批量消息/任意状态设置接口。

BeginTurnResult 保留 Conversation/History/UserMessage/AssistantMessage/Reused，并新增 `Run *agentrun.Run`。nil 只用于迁移前的 legacy 幂等返回；新执行必须有 Run。

FinishAssistantInput 保留 assistant ID、终态和受控错误字段，并新增 `ExpectedHead *conversation.HeadToken` 与 `FinalMessage *conversation.EntryMessage`。新 active Run 的任何终态写入必须提供 expected token；指针用于区分未提供和有效值，不能把 nil token 当作跳过校验的标志。成功收尾必须提供完整最终响应；失败/取消不提供 FinalMessage。不能用旧 Message 累计正文替代 FinalMessage。legacy/已终态的无写入幂等返回可以不提供 token。

### 5.2 Request / BuildInput 与 Recorder

```go
type RunRecorder interface {
    RecordAssistant(context.Context, conversation.EntryMessage) error
    RecordToolResults(context.Context, []conversation.EntryMessage) error
}
```

Request/BuildInput 增加 RunID 与 Recorder。Recorder 是本 Run 私有运行对象，保存最新 token、已提交调用的 assistant Entry ID，以及尚未终态提交的完整最终响应。

- assistant 带调用：提交后更新 token，才能执行工具。
- tool results：补充受控 CallEntryID，按声明顺序整理后提交；更新 token。
- 无调用最终响应：只在内存保存完整快照，统一收尾时交给 FinishAssistant。
- 回调可能跨 goroutine，内部状态必须串行化；两个事实提交不能使用同一个旧 token。
- 没有事务、正文重放队列或数据库草稿；这是当前 Run 的轻量内存状态。
- generationRun 持有具体 Recorder；application 内部的 finishSnapshot 返回最新 HeadToken 与最终完整响应的副本，runGeneration 将它们传入 FinishAssistantInput。无需把取快照方法加入 Graph 的 RunRecorder Interface。
- 启动/注册补偿尚未有 Recorder 时，使用 BeginTurnResult.Run 中的 LastEntryID/HeadVersion 构造 expected token；不能临时从 DB head 换成新版本。

聊天生产路径必须配置 Recorder。没有 Recorder 的旧 runtime/evaluation stub 不能在真实新 Run 中静默跳过事实写入；测试与可信离线调用可显式采用无持久化模式，不将该模式当作聊天成功写树。

### 5.3 恢复 Interface

启动恢复使用单独的窄 Interface，避免普通聊天写入调用方学习恢复操作：

```go
type RecoveryStore interface {
    RecoverInterrupted(context.Context) (RecoveryResult, error)
}

type RecoveryResult struct {
    InterruptedRuns    int
    LegacyOnlyMessages int
}
```

方法调用的前提是已获得本数据库的排他进程运行权。返回计数，不返回正文、原始事件或工具 payload。

## 6. SQLite schema 草案

编号以实现时现有 migration 为准，预计是 `0009_agent_runs.sql`。不修改已发布 0001..0008，也不删除旧表。

```sql
CREATE UNIQUE INDEX idx_messages_conversation_id_reference
    ON messages(conversation_id, id);

CREATE TABLE agent_runs (
    id TEXT PRIMARY KEY NOT NULL,
    conversation_id TEXT NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    client_message_id TEXT NOT NULL CHECK (length(client_message_id) > 0),
    user_message_id TEXT NOT NULL UNIQUE,
    assistant_message_id TEXT NOT NULL UNIQUE,

    base_entry_id TEXT,
    base_head_version INTEGER NOT NULL CHECK (base_head_version >= 0),
    user_entry_id TEXT NOT NULL,
    last_entry_id TEXT NOT NULL,
    last_closed_entry_id TEXT NOT NULL,
    final_entry_id TEXT,
    head_version INTEGER NOT NULL CHECK (head_version >= 0),

    status TEXT NOT NULL CHECK (status IN (
        'pending', 'running', 'waiting_tool', 'completed', 'failed', 'cancelled'
    )),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    UNIQUE (conversation_id, client_message_id),
    CHECK (
        (status IN ('pending', 'running', 'waiting_tool') AND finished_at IS NULL)
        OR (status IN ('completed', 'failed', 'cancelled') AND finished_at IS NOT NULL)
    ),
    CHECK (
        (status = 'completed' AND final_entry_id IS NOT NULL AND error_code = '')
        OR (status <> 'completed' AND final_entry_id IS NULL)
    ),
    FOREIGN KEY (conversation_id, user_message_id)
        REFERENCES messages(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, assistant_message_id)
        REFERENCES messages(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, base_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, user_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, last_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, last_closed_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, final_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX idx_agent_runs_one_active
    ON agent_runs(conversation_id)
    WHERE status IN ('pending', 'running', 'waiting_tool');
```

client 唯一约束保证幂等，active partial index 保证单 Run；旧 streaming assistant partial index继续保留。message 的 composite index 是外键所需的归属键，不是历史排序索引。

SQL 不证明 entry role、运行位置、状态转换或安全字段合法，仍须领域和事务校验。表只保存新 Run，旧请求重试不生成伪造 legacy Run。基于同 owner 的 deferred NO ACTION 保留整体 Conversation cascade 删除能力；必须用项目驱动验证。

## 7. 事务复用与写入控制

### 7.1 提取包内事务核心

```go
// 位于 sqlite 包内，不向 application 暴露 *sql.Tx。
func (r *EntryTreeRepository) appendTx(
    ctx context.Context,
    tx *sql.Tx,
    input conversation.AppendEntryInput,
) (conversation.AppendEntryResult, error)
```

公共 Append 继续 `WithinTx → appendTx → Commit → 返回`；Run adapter 直接在自己事务中使用 appendTx。需要等价提取 head 移动和旧 Message mutation core。

所有事务查询均使用 tx；不能在 callback 内调用公共 Append/GetHead/MoveHead 或重新从 db 取连接。单连接池下这些调用可能等待自己，分开事务则会产生部分提交。

包内函数的返回值只是临时结果，**外层 Commit 成功后才对外返回、发布 Hub 或推进 Recorder 的 token**。deferred FK 的 Commit 失败也必须返回零结果。

### 7.2 写入所有权

生产事实写入仅通过 GenerationStore 校验当前 Run；generic EntryTree port 不直接暴露给聊天 handler。生产分支导航暂不装配。

正式事实追加或 head 移动检查：

- Run 存在且属于对应旧 assistant/会话；
- Run 处于操作允许的 active 状态；
- caller expected head、Run 当前引用、数据库 head 三者匹配；
- payload 安全校验和路径完整性通过。

MarkRunning、纯文本检查点不改变树，仅校验对应 Run/Message 状态与身份，不要求每个 delta 携带 head token。启动恢复的 expected token 来自持久化 active Run，在同一恢复事务中与实际 head 比较。legacy/已终态请求的无写入幂等复用先行返回，不执行 active 或 head 写入校验。

不能只从数据库重新取最新 token 来替换 caller expected token，也不能在冲突后静默改挂新 parent。内部基础 port 不是执行租约；绕过 GenerationStore 的维护写入必须停服/取得排他运行权，不与 live Run 混用。

### 7.3 短启动/取消窗口

存在 BeginTurn 已提交、Registry 尚未注册的短窗口。相同 client ID 重试可能先拿到 assistant ID 再请求 Cancel，不能把缺少句柄误认为中断。

推荐在 Chat Service 使用一把短生命周期互斥锁，串行化 Start 的持久化/注册段与 Cancel 的读取/发送信号段。锁不覆盖模型执行、不覆盖等待 done、不替代数据库唯一约束。当前单连接数据库下无需额外设计带清理的 per-conversation 锁表。

Start 释放前完成 Hub.Open、Registry.Register 和 MarkRunning，或完成失败补偿；Cancel 获取句柄后释放锁再等待终态。不同 Conversation 的模型执行仍可并行。

## 8. 操作流程

### 8.1 BeginTurn：创建或复用

同一事务内：

1. 校验规范化的 conversation/client ID 与非空文本；加载会话。
2. 查 agent_runs 的 conversation+client ID；已有则返回原 Run 和旧 Message，不执行模型。
3. 若有旧 user/assistant Message 对但没有 Run，返回 legacy Reused=true/Run=nil；缺少配对记录返回一致性错误，不重新生成。
4. 幂等检查后才检查 active Run 和旧 streaming assistant；新 client ID 返回 Conflict。
5. 读取 head/path，要求当前路径完整且闭合；保存 base leaf/version。
6. appendTx 创建 user Entry并推进 head。
7. 创建旧 completed user/streaming assistant，保留现有 sequence/history 语义。
8. 创建 pending Run，last/last_closed=user，head_version=user 提交后的 version。
9. 更新会话时间并提交。

Run unique index 或任一步骤失败，head、Entry、Message、Run 一起回滚。

同 client ID 不同文本暂沿用现有语义：返回原结果，忽略新正文，不覆盖、不再次执行。旧请求不回填 Run/树；旧历史未写入树的事实必须可诊断，不假装树覆盖全部历史。

### 8.2 注册、MarkRunning 与启动补偿

新 BeginTurn 成功后创建与 HTTP 解耦的运行上下文和 Recorder，Open Hub，注册取消句柄，再以状态条件 pending→running。

```sql
UPDATE agent_runs SET status = 'running', started_at = ?
WHERE id = ? AND status = 'pending';
```

更新成功后才能调用 Executor。注册、状态转换或 goroutine 启动准备失败时，用独立短超时上下文原子标记 Run/旧 assistant failed，发送受控终态并清理句柄；不得遗留永久 busy 记录。

若补偿本身写库失败，不能广播虚假终态或开启模型；保留可诊断 active 记录，由显式维护/下一次启动恢复处理。

### 8.3 文本检查点

AppendAssistantText 仍只修改旧 Message。在事务中加载对应 Run，要求 active 且 Message streaming；成功提交后 Hub.PublishText。没有 Run 的旧 terminal 重试不进入这个写入路径。

文本 delta 不进入 Entry Tree，不向 Run 复制一份正文，不以 delta 次数推进 head version。

### 8.4 提交 assistant 工具调用

Graph 提供一条已经完整收敛的 assistant 响应。先检查工具轮数和安全策略，再在一个事务中：

```text
校验 running + expected head
  → 追加完整 assistant Entry
  → LastEntryID/HeadVersion 前进
  → Run waiting_tool
  → Commit
```

LastClosedEntryID 保持组前闭合点。调用记录成功后才允许 ToolsNode 执行。记录失败则 Graph 返回错误，工具不得启动。

### 8.5 提交工具结果

以 CallID 配对，验证数量、名称、assistant identity、错误标记和白名单；按原 assistant 声明顺序整理。首版等待本组全部结果再提交，不按并行完成先后落树。

```text
校验 waiting_tool + expected head
  → append result(c1)
  → append result(c2)
  → 更新 LastEntryID/LastClosedEntryID/HeadVersion
  → Run running
  → Commit
  → 允许下一轮模型
```

这只是一次事务中的多个独立 Entry，不创建 tool_result_batch。任意失败全部回滚。工具可能已经产生副作用，事务失败不能自动重执行工具；Run 进入受控失败收尾。

### 8.6 最终响应与终态

完整无工具 assistant 由 Recorder 保存；Executor 正常结束后，generation goroutine 在独立 finalize context 中先 Flush，再 FinishAssistant。

成功收尾同事务：校验 Run running/head → 校验 FinalMessage → append 最终 assistant → Run completed/FinalEntryID/FinishedAt → 旧 assistant completed → 更新会话时间 → Commit。

无完整最终响应、流截断、非法调用参数、模型失败或最后 Flush 失败不能伪装 completed。失败/取消不写未完成 assistant Entry，保留旧可见正文。

终态方法先读取 Run/Message：相同终态重试直接复用既有结果，不重复追加最终 Entry，也不使用 terminal Run.LastEntryID/HeadVersion 重建可写 token；不同终态返回 Conflict，不修改已有终态。只有仍 active 的 Run 才检查输入 expected token并尝试终态事务。

终态竞争采用状态条件，只能一个成功。Cancel 等待后重新读取实际 Message，所以自然完成赢过取消时可以返回 completed。只有提交成功才 Hub.Complete。

### 8.7 未闭合组失败/取消

例：

```text
U1 → A1(c1,c2)   当前 waiting_tool
```

终态事务校验 Run token/head 和合法 LastClosedEntryID，CAS 移动 head 至 U1，version+1，同时更新 Run failed/cancelled 和旧 assistant 终态。

A1 不改、不删；Run.LastEntryID 仍为 A1，LastClosedEntryID=U1，FinalEntryID=nil，HeadVersion 记录回退后的版本。下一次新 client ID 可在 U1 后追加 user。

完整结果组已提交时 LastClosedEntryID=LastEntryID，不额外移动 head。任何未知 head、损坏路径或失效 token必须报一致性错误，不能“为了释放 busy”强行改挂其他分支。取消不保证工具无外部副作用，错误展示不能声称已经执行的工具被撤销。

## 9. Eino 完整消息接线

展示 callbacks 保留 step/text/tool 摘要，但不承担必须成功的事实记录，因为现有通知路径可能忽略 WriteEvent 错误。

控制路径应是：

```text
完整模型响应
  → 校验 stop/参数完整性、工具轮数
  → 映射 user/assistant/toolResult 支持范围
  → 安全投影 + RecordAssistant
  → 执行工具
  → 验证/安全投影/整理完整结果
  → RecordToolResults
  → 下一轮模型
```

可以扩展现有 recordToolCall、recordToolResults、routeModelOutput 或加入显式 Graph 控制节点。实现方法须满足：

- 每条完整响应只记录一次；不能同时从展示 callback 和主 stream 重复记录。
- 消费私有流副本并聚合完整 Content/ToolCalls，保留 UI delta 实时转发。
- 持久化错误返回 Graph/Executor；不能使用忽略错误的通知通道。
- Eino schema.Message 的 Content/ToolCalls 是当前来源，不能宣称能恢复 SDK 未提供的任意交错 block 顺序。
- 内部 thinking 不映射到 Entry；unsupported/incomplete 响应明确失败。
- 当前 Graph 执行继续使用内存中的完整参数/结果，不从 SafeToolCall 重新执行工具。
- 生产工具策略在启动时注册；每个工具的允许字段与版本需审计。未注册策略失败关闭，不新增原文 fallback。

工具结果 CallEntryID 来自 Recorder 最近成功提交的 assistant Entry，而不是从 UI ToolTitle 或任意历史 call ID 猜测。

## 10. 取消、关闭与恢复

### 10.1 取消

HTTP Cancel 沿用 assistant ID → 读取 Message/定位 Run → Registry.Cancel → 执行器观察 ctx.Done → generation goroutine统一 Flush/Finish → Registry.Complete → 返回实际终态。

Cancel 不直接写终态。等待 done 不持有生命周期锁；自然完成可能赢过取消。迟到文本/事实提交由 Run active 和 Message streaming 条件拒绝。

Hub 缺失不能触发失败判断；Subscribe 对 DB 快照的降级不是执行中断证明。

### 10.2 服务关闭

Run 不以 HTTP request ctx 为父级，但必须继承进程 root 的取消。DetachedRunContextFactory 应从 root 派生取消/超时，而不是用 WithoutCancel(root) 把服务关闭信号一起丢掉。

先停止接受新 Start，取消/等待活跃 Run，再关闭 DB。统一终态使用独立短 finalize context，避免取消信号阻止写入取消状态。超过关闭宽限仍未收敛的记录由下次恢复处理，不续执行。

### 10.3 启动恢复与单实例运行权

在服务迁移和恢复前取得每个数据库的排他进程运行权，整个进程生命周期保持至退出。推荐 OS 级自动释放的文件锁；仅创建 O_EXCL 标记文件会遗留 stale lock，不满足此要求。锁库/平台实现属于后续代码 review 细节，不引入多实例 lease/调度系统。

恢复事务：

- 查 pending/running/waiting_tool 的 Run；检查关联 Message、head 和路径。
- 对未闭合组执行同样的闭合点 CAS 回退。
- Run 和关联 streaming assistant → failed/generation_interrupted，保留可见文本与已提交 Entry。
- 对没有 Run 的旧 streaming assistant执行 legacy-only 收敛，不补造完整 Entry或新 Run。
- 更新相关会话时间，一次提交；失败阻止服务就绪。

重复恢复无新变更，返回计数。无法证明归属、发现 head 与 Run 不匹配时必须失败关闭，不能标记其他仍活跃进程的数据为中断。

## 11. 兼容、删除与安全

### 11.1 外部兼容

StartResult 保留 ConversationID/UserMessageID/AssistantMessageID，不强制输出 Run ID。订阅和取消 URL、AI SDK 编码保持原样。History 仍由服务端旧 completed Message 查询生成，客户端不能提供替代 history。

生产装配没有分支导航；树内部失败分支不进入未来 active-path Projection。Phase 3 切换前不能把全树节点或旧累计可见正文冒充正式工具协议。

### 11.2 删除

整体 Conversation 仍可删除，关联 Run、Entry/head 和旧 Message一起清理。生产删除入口需先取消相关活跃执行/串行化启动窗口，再按受控流程删除；晚到操作不能重新创建会话或推进已删除 head。

Run 绑定的单条 Message不能破坏 Run 引用链；本阶段推荐返回明确 Conflict，保留整体会话删除能力。该外部行为需在实现前单独 review，不用外键错误冒充已完成 API 设计。Phase 1 单节点不可变策略保持不变。

### 11.3 安全与诊断

仅记录 run/conversation/message/entry ID、状态、计数、受控错误分类与 head version。不记录模型输入、完整工具 JSON、原始错误、凭据或中间推理。

安全字段仍可能含敏感值，白名单是逐工具审计契约，不是通用自动脱敏。preview 必须从批准字段生成且截断标记一致；注册策略不可被外部 slice 修改。

稳定错误分类建议：invalid_input、busy、entry_conflict、tool_policy_invalid、model_failed、tool_failed、message_persist_failed、generation_interrupted、generation_start_failed；最终命名在 code review 固定，不直接暴露 provider 错误。

## 12. 自顶向下代码计划

| 切片 | 内容 | 完成门禁 |
| --- | --- | --- |
| 0 | 同步本设计与 OpenSpec、审查 Schema/状态/兼容契约 | 文档一致，不宣称代码完成 |
| 1 | Run 类型、状态纯函数、GenerationStore/Recorder 骨架、fixtures | 已实现并通过编译/测试；旧代码未装配新路径，等待用户 review |
| 2 | 提取包内 append/move/message Tx core | Phase 1 行为及测试不变，无嵌套事务 |
| 3 | migration、BeginTurn 与 pending Run | 幂等、active 索引、创建/Commit 故障回滚 |
| 4 | MarkRunning、生命周期窗口、文本门禁、终态/补偿 | 注册失败不遗留可执行假成功、终态唯一 |
| 5 | 完整消息聚合、工具政策、Recorder/Graph 接线 | 写入失败阻止工具/下一模型，声明顺序完整 |
| 6 | 未闭合组收尾、root 取消、排他启动恢复 | 重启不重放，恢复失败不就绪 |
| 7 | legacy 重试、删除、HTTP/SSE 和模型 stub 回归 | 兼容验收与独立 review 后才切生产装配 |

建议文件：

- 新增 `domain/agentrun/model.go`、`state.go` 和单元测试。
- 修改 `application/chat/{ports.go,types.go,service.go,stream.go,generation.go}`，新增 `run_recorder.go`。
- 修改 sqlite EntryTree/ChatTurn 的事务实现，新增 `agent_run_store.go` 与恢复私有 helper。
- 新增下一版本 migration与 sql_test Run/故障注入测试。
- 修改 Eino完整消息 adapter、interview builder及对应 stub/流式测试。
- 修改 `cmd/server/main.go` 的恢复/关闭装配，增加必要的排他运行权实现与测试。

每个切片汇报、review 后再继续；本次文档落地不等于批准直接修改整条链路。

## 13. 测试与验收

### 13.1 Domain

- 六状态合法/非法转换；terminal 不可恢复为 active。
- LastClosed/Last/Final 的引用与 pending/completed 约束。
- Run/Recorder 返回值深拷贝、并发调用串行化、失败不更新 token。

### 13.2 SQLite

- 同 client 一条 Run、一条 user Entry、一对旧 Message；旧 client 只复用。
- 同会话新 client 只能一个 active，不同会话允许独立执行。
- 初始、调用、结果组、最终响应事务任一步及 Commit 失败均回滚、返回零值。
- head ABA、Run token 与 head 不一致、跨会话引用、缺失/损坏路径失败。
- tool results声明顺序；任意坏结果不保留半组。
- registered policy 原文/preview 旁路、超限、不支持版本拒绝。
- 未闭合失败分支保留；head 回到闭合点，version 单调增加，新请求可继续。
- 相同终态幂等，不同终态不能覆盖；late text/fact 不写库。
- Run/Message/Entry整体 cascade，单条 Message删除明确受控。

### 13.3 Runtime / HTTP

- 调用事实提交失败，工具执行次数为零；结果提交失败，下一模型次数为零。
- 7th工具轮不执行；完整最终响应仅提交一次，不从累计可见正文伪造。
- stub 也通过明确完整消息 seam，不以缺 Recorder 的成功掩盖未写树。
- Start 注册窗口重试/Cancel race、失败补偿、浏览器断连继续执行。
- 服务 root 取消传播；完成/取消/错误竞争；只在 Commit 后广播 terminal。
- SSE 同锁注册与复制快照，先 snapshot 再增量；不承诺 queue 永不溢出或事件重放。

### 13.4 恢复

- pending/running/waiting_tool和 legacy-only streaming 全部收敛。
- 整个恢复失败不接受请求；第二实例拿不到运行权时不恢复别人的 Run。
- 重复恢复无副作用；旧 client返回中断结果，新 client创建新 Run。
- 已执行工具不自动重放，失败分支不删除。

实现后从 backend module运行对应 domain、sqlite、chat、Eino/server 测试和 `go test -count=1 ./...`；支持的环境运行 race。最初文档轮未实现或运行 Phase 2 Go 测试；当前切片 1 的检查见 §13.6，不代表其余 Run 集成检查已通过。

### 13.5 本轮文档检查记录

从本文提取 schema 草案，在 Node `node:sqlite` / SQLite 3.53.3 的临时内存库中检查：第二个 active Run、重复 client key、跨会话 Entry 引用、缺少 final Entry 的 completed 被拒绝；旧 Run 终态后可创建新 Run；整体会话 cascade 后 foreign_key_check 无错误。

未连接项目数据库，未执行真实 migration，也未运行 Phase 2 Go 实现测试。此记录只验证 SQL 草案的相关行为，不替代 modernc.org/sqlite 集成测试、状态机或业务验收。

### 13.6 切片 1 实现与检查记录

- 新增 `domain/agentrun/{model,state}.go` 及测试：六状态、转换、仅引用 Run、结构校验、深拷贝、随机 ID、仅 active Run 可构造 head token。
- `ValidateRun` 只检查本地结构。初始 user 版本为 base+1；存在后续事实时至少推进两次且必须有启动时间。归属、闭合祖先及实际 head 一致性仍须后续事务验证。
- 时间检查存在性，不以墙钟先后拒绝快照，避免系统校时/重启导致合法终态无法收敛。ErrorCode 暂仅检查有界分类格式；批准分类白名单和安全文案映射在运行接线前定稿。
- 扩展 `application/chat/{ports,types}.go`：GenerationStore、RunRecorder、RecoveryStore；Run/expected token/完整最终消息及输入输出 Clone。旧 TurnStore 与生产调用路径保留不变。
- 契约 fixtures 只做编译检查，不提供成功假实现。具体 Recorder、串行化、持久化失败不推进 token 及 Executor 转发尚未实现。
- 定向测试及 `cd backend && go test -count=1 ./...` 通过；独立 review 的版本下限和未启动终态引用问题已修复并补充反例测试。
- `go test -race -count=1 ./internal/domain/agentrun/... ./internal/application/chat/...` 未能运行：当前环境未启用 CGO；不宣称 race 通过。OpenSpec CLI 不可用，未执行 CLI validate。
- 不勾选包含 schema/adapter 的复合 OpenSpec 任务，不宣称 Phase 2 已完成。切片 2 及后续修改仍需用户批准。

## 14. 风险、回滚与未决实现细节

| 风险 | 控制 |
| --- | --- |
| Run/Entry/read model部分提交 | 一个事务、内部 Tx helper、Commit 后返回 |
| callback忽略写入错误 | 正式事实走可传播错误的控制路径 |
| 用户取消早于 Registry 注册 | 短生命周期锁，等待不持锁 |
| 失败工具组堵住下次提交 | 保留失败分支、CAS 回最近闭合点 |
| 两进程互相恢复 | 整个生命周期排他运行权 |
| 原始工具数据泄漏 | 版本策略、封闭安全值、写入校验、无 raw fallback |
| head变了但仍用旧 history | 不开放业务分支，Phase 3 才切 Projection |
| schema升级后旧二进制启动失败 | 备份、版本兼容 review，优先 forward fix |

装配切换应在所有必需切片完成后进行。上线后不能在数据库已有 Run 事实时直接换回只写旧 Message的 writer，让两套生命周期静默分叉。暂停新提交/收敛 active Run、保留数据，再做显式兼容回退或 forward fix；不 DROP TABLE，不重写已发布 migration。

已确认的是阶段范围与三项核心选择，不等于每行代码已经 review。开工前仍须定稿：具体工具白名单及版本、OS锁实现、Run绑定 Message 单条删除的外部行为、完整 Eino响应聚合 seam与受控错误分类。它们是上述已批准方案的实现检查点，不是新增 artifact/replay/Projection 功能。
