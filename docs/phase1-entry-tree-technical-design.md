# Phase 1 技术设计：Conversation Entry Tree

> 状态：Phase 1 基础实现已落地；本文保留设计契约，Go/SQL 示例不保证逐字对应当前源码。
> 所属变更：[refactor-conversation-message-tree](../openspec/changes/refactor-conversation-message-tree/design.md)。
> 后续设计：[Phase 2 AgentRun 生命周期与聊天接入](phase2-agent-run-technical-design.md)。本次文档同步不修改业务代码。

## 1. 目标和阶段边界

### 1.1 Phase 1 交付什么

建立一个独立、可测试的 Entry Tree 模块：

- 用稳定 Entry ID 和 `ParentID` 保存会话事实；
- 用一个会话 head 保存 `ActiveLeafID + Version`；
- 以事务追加节点并推进 head；
- 从任意 leaf 读取 root-to-leaf 路径；
- 移动 head 后继续追加，形成分支，不复制旧历史；
- 保存 user、assistant、独立 toolResult 的类型化消息；
- 在持久化工具数据前执行工具级白名单投影；
- 验证孤立、重复、乱序工具结果；
- 保留原来的聊天写模型和 HTTP/SSE 行为。

### 1.2 Phase 1 不交付什么

不接入 ChatTurn、AgentRun、模型调用、ContextManager、Eino converter、token 预算或 compaction。也不新增：

- Draft 表；
- 持久化 SSE 事件重放、revision 跳号或复杂重同步协议；
- artifact 文件存储、TTL、配额、读回工具；
- 具名 Branch 表、分支合并、前端树 UI；
- 旧消息回填和双写；
- thinking、图片、system prompt 原文的持久化。

未来的 compaction/context edit 等 Entry 类型通过新版本扩展，不在 Phase 1 提供可写入但没有行为定义的空壳类型。

### 1.3 与此前规划的差异

最初 OpenSpec 曾将 Compaction、artifact 生命周期和 system/thinking 通用能力混入基础阶段。本次文档同步按已确认决定对齐为：

1. Phase 1 实际写入只支持 `kind=message` 和三种角色；
2. 安全工具投影从首个工具 Entry 开始强制执行，不能先存原文再补隐私；
3. artifact、compaction 等属于后续 capability；
4. SSE 保持当前锁内“注册订阅者 + 复制快照”，不在本阶段改造。

proposal/design/spec/tasks 已按这些阶段边界同步；这不是未来 Run/Projection/Compaction 已实现的声明。产品交付阶段 MVP/Phase 2 与这里的实施步骤 Phase 1/2 不是同一套编号，详见 Phase 2 文档阶段映射。

## 2. 仓库现状与为什么需要新模型

| 现有位置 | 当前职责 | Phase 1 处理 |
| --- | --- | --- |
| `backend/internal/domain/conversation/model.go` | 可见 `Message`：user/assistant、Content、Sequence、Status | 保持不变，另建 Entry 类型 |
| `backend/internal/infrastructure/repository/sqlite/chat_turn_store.go` | BeginTurn 预创建空 assistant，分批追加正文，最后收敛终态 | 保持不变 |
| `backend/internal/application/chat/stream.go` | 默认累计 64 字节或等待 80ms 后提交文本，先落库再广播 | 保持不变 |
| `backend/internal/application/chat/generation.go` | 活跃生成快照、事件摘要、SSE subscribers，同锁订阅与快照 | 保持不变 |
| `backend/internal/agent/interview/builder.go` | 单次 Graph 内保存 assistant ToolCalls 和独立 ToolMessage | 暂不接入树 |
| `backend/internal/agent/eino/executor.go` | 从旧 user/assistant 文本恢复模型 history | 暂不替换 |
| `backend/internal/infrastructure/storage/migrations/0001..0007` | 旧消息、幂等键、事件表定义、线性摘要表 | 不修改已发布文件 |

当前 SQLite 使用 WAL、foreign_keys、busy_timeout 和单连接池，事务由 `storage.DB.WithinTx` 管理。migration 按版本顺序和 checksum 校验；新增 migration 的实际编号在实现时重新检查，不能固定假设一直是 0008。

旧 `conversation.Message` 继续是当前聊天的事实/显示来源；新 Entry Tree 在 Phase 1 中是尚未接入业务的独立设施。不能因为新表存在，就宣称历史接口已有工具轨迹、分支或压缩。

## 3. 架构与数据粒度

### 3.1 模块关系

```text
未来的 AgentRun / ContextManager（本阶段不接入）
                    │
                    ▼
          Entry Tree Interface
  Append / MoveHead / ReadPath / Get / ListChildren
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
 conversation domain    SQLite EntryTreeRepository
 payload/工具校验        codec + 事务 + CAS + 查询
                              │
                              ▼
                 entry_nodes + entry_heads

现有 ChatTurn → messages → MemoryGenerationHub → SSE
                    （本阶段完整保留）
```

domain 不依赖 Eino 或 `database/sql`。Store port 放在 domain/conversation，符合现有 ConversationStore/MessageStore 的组织方式。SQLite adapter 完成事务、JSON 和时间适配。不要另外增加纯透传 application service；后续真正接入 AgentRun 时再增加编排。

### 3.2 三种不同粒度

| 对象 | 含义 | 示例 |
| --- | --- | --- |
| 流式 delta | 传输增量 | Markdown 每次几个字；参数 JSON 的一小段 |
| ContentPart | 一条消息内的内容块 | text、toolCall |
| ConversationEntry | 已收敛的一条消息事实 | 一次 assistant 响应或一个 toolResult |

大 Markdown 的几千个 delta 累积到当前 text block，不生成几千个 ContentPart/Entry。尊重模型的 block 起止和次序，不跨不同 block 强行合并文本。

```text
U1: user
 ↓
A1: assistant [text("先查询"), toolCall(c1), toolCall(c2)]
 ↓
R1: toolResult(c1)
 ↓
R2: toolResult(c2)
 ↓
A2: assistant [text("完整 Markdown 报告")]
```

ToolCall 内嵌 assistant；每个 ToolResult 独立成 Entry。并行执行顺序与消息逻辑顺序不同：后续工具执行模块负责按 assistant 声明次序提交结果，Phase 1 负责验证并拒绝不合法次序，不执行或重新排序工具。

### 3.3 不可变范围

已插入 Entry 的 ID、会话、parent、kind、payload version、payload、depth、append index、时间不可修改；没有 streaming Entry。Go 值可以被调用方修改，因此实现需复制 pointer/slice，codec 必须冻结写入快照；不能把“Go struct”误称为语言级不可变对象。

允许变化的是 head 的 leaf/version，而不是 Entry。整体删除 Conversation 仍必须删除其树和 head，这是数据生命周期/用户删除权，不是对单条历史的业务编辑。不得用无条件禁止 DELETE 的 trigger 破坏会话删除。

## 4. Go 类型设计

以下类型放在 `conversation` 包中。采用 `EntryRole` 和 `EntryMessage`，**不重声明已有 MessageRole/Message**。

### 4.1 Entry 与 head

```go
// import "time"
type EntryID string
type EntryKind string

const EntryKindMessage EntryKind = "message"
const EntryPayloadV1 = 1

type ConversationEntry struct {
    ID             EntryID
    ConversationID string
    ParentID       *EntryID // nil = 会话虚拟根前；不是 *ConversationEntry
    Kind           EntryKind
    PayloadVersion int
    Message        EntryMessage
    AppendIndex    int64    // 仅物理追加顺序
    Depth          int64    // root=0；child=parent.Depth+1
    CreatedAt      time.Time
}

type EntryHead struct {
    ConversationID string
    ActiveLeafID   *EntryID
    Version        int64
    UpdatedAt      time.Time
}

type HeadToken struct {
    ActiveLeafID *EntryID
    Version      int64
}
```

ID 使用现有 `crypto/rand + hex` 风格并加 `e_` 前缀，无需新依赖。测试可以注入 ID/clock。CreatedAt 使用 UTC RFC3339Nano；不对既有 Entry 自动 normalize。repository 分配 AppendIndex 和 Depth；调用者不能给它们随意赋值。

head Version 必须由调用者随 expected leaf 一起提交：`P(v5) → Q(v6) → P(v7)` 后，旧请求 `P(v5)` 仍冲突，避免 ABA。只比较 leaf ID 无法实现这一点。

root 是 parent=nil 的实际消息节点；允许将 head 显式 reset 到 nil 后追加新的 root，等价于 Pi 回到第一条消息之前。一个 Conversation 可保存多条根路径，它们共享会话虚拟根；读取时只选择指定 leaf，不把所有 root 混成历史。首次 root 默认只允许 user 消息。

### 4.2 消息与 content blocks

```go
type EntryRole string
const (
    EntryRoleUser       EntryRole = "user"
    EntryRoleAssistant  EntryRole = "assistant"
    EntryRoleToolResult EntryRole = "toolResult"
)

type EntryPartType string
const (
    EntryPartText     EntryPartType = "text"
    EntryPartToolCall EntryPartType = "toolCall"
)

type EntryStopReason string
const (
    EntryStopNormal EntryStopReason = "stop"
    EntryStopTools  EntryStopReason = "toolUse"
)

type EntryMessage struct {
    Role       EntryRole
    Content    []EntryPart
    ToolResult *EntryToolResult // 仅 toolResult role 使用
    StopReason EntryStopReason // 仅 assistant 使用
}

type EntryPart struct {
    Type     EntryPartType
    Text     *string       // pointer 保留“空 text”与“不存在”的区别
    ToolCall *SafeToolCall
}

type EntryToolResult struct {
    CallEntryID EntryID        // 请求该工具的 assistant Entry
    CallID      string
    ToolName    string
    IsError     bool
    Result      SafeToolResult
}
```

Part 是 tagged union：text 必须只有 Text，toolCall 必须只有 ToolCall，不能两个字段都有。user 只允许 text；assistant 允许有序 text/toolCall；toolResult 使用 ToolResult，Content 为空，结果预览由 Result 提供，避免同一结果保存两份正文。

assistant 有工具时 StopReason 必须是 toolUse；无工具时为 stop。本阶段不接受 pending、未闭合参数、provider 截断调用、aborted partial 的持久化消息。模型终态、失败部分内容和客户端可见消息如何映射，是后续 Run 集成评审事项；旧 messages 已保存的失败/取消文本不受影响。

system/thinking/image/artifact 是未来版本能力，不添加一个默认可序列化但“不建议保存”的 Thinking 字段。

### 4.3 安全工具数据

不把原始 arguments JSON 直接塞入 Entry；不保存 `ArgumentsText` 或 arbitrary `map[string]any`。

```go
type SafeField struct {
    Name  string
    Value string // v1 仅有界标量字符串，不支持任意嵌套对象
}

type SafeToolCall struct {
    id            string
    name          string
    policyVersion string
    fields        []SafeField
}

type SafeToolResult struct {
    policyVersion string
    fields        []SafeField
    preview       string
    truncated     bool
}

type ProjectionFieldRule struct {
    SourceKey string
    StoredKey string
    MaxBytes  int
}

type ToolPersistencePolicy struct {
    ToolName       string
    Version        string
    ArgumentFields []ProjectionFieldRule
    ResultFields   []ProjectionFieldRule
    MaxPreviewBytes int
}
```

Safe 类型内部字段不导出，只能通过投影构造函数和受控存储解码器创建；访问器返回副本，不泄露内部 slice。自定义 JSON codec 负责 serialize/deserialize，不使用默认 Marshal 产生空对象。

构造函数契约（签名示意；raw 只在内存中使用，不属于 Entry）：

```go
// import "encoding/json"
func ProjectSafeToolCall(
    policy ToolPersistencePolicy,
    callID string,
    rawArguments json.RawMessage,
) (SafeToolCall, error)

func ProjectSafeToolResult(
    policy ToolPersistencePolicy,
    rawResult json.RawMessage,
) (SafeToolResult, error)
```

投影规则：

1. 工具名和策略版本必须已注册；未知策略默认拒绝，不使用通用“猜测脱敏”。
2. 只提取显式配置的顶层标量字段，输出按照策略声明顺序，未声明字段丢弃。
3. 不复制嵌套对象、不从 rawResult 自动截取前 N 字节当 preview。
4. preview 仅从已批准字段生成；原始错误替换成受控错误分类。字段值仍可能敏感，白名单不是自动隐私证明，必须做逐工具 review。
5. 调用字段超限直接返回 Capacity，不截断后假装参数完整；结果字段/预览可以按 UTF-8 有界截断，任何截断都将 SafeToolResult.truncated 标记为 true。结构参数缺失可以拒绝，安全摘要本身仍不用于执行工具。
6. 敏感 SourceKey/StoredKey（Authorization、cookie、secret 等）在 policy 注册时拒绝；测试验证敏感字段不会出现在编码结果中。
7. 相同策略、相同输入产生相同安全快照；codec 回读保留 policyVersion，不以新策略静默重写旧 Entry。

**安全投影不是完整工具协议重放。** 丢弃字段后的 SafeToolCall 不能直接当作原始函数 arguments 再执行。后续 Context Projection 必须明确选择“作为历史事实文本表达”或其他经 review 的合法映射；不能承诺可逐字恢复 provider 请求。当前 Run 的工具执行继续使用内存中的完整参数。

工具安全投影是 Phase 1 的必要写入边界，真实工具接线和 artifact 存储不是。若结果超出可保留内容，Phase 1 返回有界 preview/truncated，不生成一个无法读回的虚假 artifact ID。

### 4.4 校验函数与错误

```go
// 纯函数；内部不读数据库，不执行工具。
func ValidateEntryMessage(message EntryMessage) error
func ValidateToolContinuation(
    parentPath []ConversationEntry,
    next EntryMessage,
) error
```

这些是契约签名，不是已实现函数。

- role、part union、StopReason、ID/名称不能为空或非法；
- assistant 内多个 call ID 不重复；同一 root-to-leaf 路径内也不复用已出现的 call ID，避免后续 provider 转换歧义；不同兄弟分支可以保留各自的相同调用身份；
- toolResult 的 CallEntryID/CallID/ToolName 必须对应当前未闭合调用组；
- 不允许 duplicate、orphan、错序结果或新 user/assistant 越过 pending group；
- 允许 assistant call 已完成但工具尚未返回，此时树暂时有 pending group；“消息完成”不等于“工具交互组完成”。

复用 `domainerr.ErrInvalidInput/ErrNotFound/ErrConflict`。新增 tree 专属 `ErrEntryCorrupt`、`ErrEntryUnsupportedVersion`、`ErrEntryCapacity`；名称在实现 review 时定稿，不假称仓库已存在。错误携带 Entry ID 和有限原因枚举，不附 payload、原始工具错误或主机路径。

## 5. Entry Tree Interface 和代码分工

```go
// import "context"
type EntryTree interface {
    GetHead(context.Context, string) (EntryHead, error)
    Get(context.Context, EntryRef) (ConversationEntry, error)
    ReadPath(context.Context, PathQuery) (EntryPath, error)
    ListChildren(context.Context, ChildrenQuery) (ChildrenPage, error)
    Append(context.Context, AppendEntryInput) (AppendEntryResult, error)
    MoveHead(context.Context, MoveHeadInput) (EntryHead, error)
}

type EntryRef struct {
    ConversationID string
    ID             EntryID
}

type PathQuery struct {
    ConversationID string
    LeafID         *EntryID
    UseActiveLeaf  bool // true: 同一个读取快照内取 head，LeafID 必须 nil
}

type EntryPath struct {
    Head    EntryHead             // 读取时的 head，不代表之后永不变化
    LeafID  *EntryID
    Entries []ConversationEntry   // root-to-leaf
}

type ChildrenQuery struct {
    ConversationID string
    ParentID       *EntryID
    AfterIndex     int64
    Limit          int
}

type ChildrenPage struct {
    Entries   []ConversationEntry
    NextIndex *int64
}

type AppendEntryInput struct {
    ConversationID string
    ExpectedHead   HeadToken
    Message        EntryMessage
}

type AppendEntryResult struct {
    Entry ConversationEntry
    Head  EntryHead
}

type MoveHeadInput struct {
    ConversationID string
    ExpectedHead   HeadToken
    TargetID       *EntryID // nil = 回到第一条消息之前
}
```

契约：

- Append 的 parent 从 ExpectedHead 推导，调用者不同时提交两份可能不同的 ParentID；
- ExpectedHead 中 version 必须来自此前读到的 head，不能由 repository 换成“当前版本”代替调用者检查；
- ID/UTC 时间通过 repository 构造依赖注入生成，错误返回前不发布 Entry；
- AppendEntryResult 同时返回新 head，方便下一次工具结果使用新的 token；
- ReadPath 必须在同一读取快照内取 head/节点，不在多次不同快照之间拼接；
- 所有读操作都带会话归属；Entry ID 不是访问其他会话的授权手段；
- ListChildren 按 append index 分页，用于观察树，不是模型 history；
- 空会话返回 head(nil, version=0)、空路径；不存在的 conversation 返回 NotFound；
- 同一次成功追加的 ID 重试/重复插入不得覆盖；client id 幂等属于后续 AgentRun，Phase 1 不默默重执行冲突追加；
- MoveHead 成功后 version 增加；目标就是当前 leaf 时仍做 CAS，随后可作为一次有序 head 操作返回新版本；
- 不提供 UpdateEntry/DeleteEntry/ExecuteTool/ReplayEvents 接口。

SQLite adapter 构造接口建议：

```go
// 放在 sqlite 包；Entry ID generator/clock 可在测试中替换。
type EntryTreeOptions struct {
    NewID           func() (conversation.EntryID, error)
    Now             func() time.Time
    MaxPayloadBytes int
    MaxPathEntries  int
}

func NewEntryTreeRepository(
    db *storage.DB,
    options EntryTreeOptions,
) (*EntryTreeRepository, error)
```

容量不是任意隐藏常数：初始提议 MaxPayloadBytes=1 MiB、MaxPathEntries=10000、Children Limit=100（最大500）。这些是 **待 review 的有界保护起点**，不是已经批准的产品上限；超限返回明确容量错误，不截断消息或只返回半条上下文。

## 6. SQLite schema

### 6.1 Schema 草案

下述 SQL 是文档内可验证的 schema 草案，不会在本轮执行项目迁移。

```sql
CREATE TABLE conversation_entry_nodes (
    id TEXT PRIMARY KEY NOT NULL,
    conversation_id TEXT NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    parent_id TEXT,
    kind TEXT NOT NULL CHECK (kind = 'message'),
    payload_version INTEGER NOT NULL CHECK (payload_version = 1),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    append_index INTEGER NOT NULL CHECK (append_index > 0),
    depth INTEGER NOT NULL CHECK (depth >= 0),
    created_at TEXT NOT NULL,
    UNIQUE (conversation_id, id),
    UNIQUE (conversation_id, append_index),
    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK ((parent_id IS NULL AND depth = 0)
        OR (parent_id IS NOT NULL AND depth > 0)),
    FOREIGN KEY (conversation_id, parent_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE conversation_entry_heads (
    conversation_id TEXT PRIMARY KEY NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    active_leaf_id TEXT,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at TEXT NOT NULL,
    FOREIGN KEY (conversation_id, active_leaf_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX idx_entry_children
    ON conversation_entry_nodes(conversation_id, parent_id, append_index);

CREATE TRIGGER entry_nodes_no_update
BEFORE UPDATE ON conversation_entry_nodes
BEGIN
    SELECT RAISE(ABORT, 'entry_immutable');
END;

CREATE TRIGGER entry_nodes_no_individual_delete
BEFORE DELETE ON conversation_entry_nodes
WHEN EXISTS (
    SELECT 1 FROM conversations WHERE id = OLD.conversation_id
)
BEGIN
    SELECT RAISE(ABORT, 'entry_immutable');
END;
```

说明：

- composite FK 同时约束 ID 和 conversation，不能跨会话挂 parent/head；
- deferred NO ACTION 在事务提交检查自引用/头引用，不用立即 RESTRICT 阻塞整会话级联删除；
- UPDATE trigger 保护正式 Entry，不对 heads 加不可变 trigger；
- DELETE trigger 拒绝会话仍存在时的单节点删除，允许 conversations 删除触发的整体 cascade；必须使用真实 SQLite 验证 cascade 顺序；
- schema 只验证 JSON 合法，具体 role/parts/policy/schema 一致性由 codec + domain 校验，不认为 json_valid 证明工具数据安全；
- 不对业务查询引入大量没有调用方的 kind/时间索引；后续有测量依据再增加；
- future Entry 类型和 payload 版本需要新增 migration/codec，不修改已有 migration。

### 6.2 Head 初始化

Phase 1 不改 ConversationRepository.Create。采用**首次树写入时懒初始化**：

1. GetHead 对已有 Conversation 且不存在 tree/head 返回逻辑空 head；
2. 第一次 Append/MoveHead 在事务内创建 head(nil,0)；
3. 若节点已存在但 head 缺失，返回 Corrupt，不能把非空树默认为新树；
4. 从 migration 前已有或 migration 后新建的 Conversation 都能开始使用树，不依赖更新旧 Create；
5. Phase 1 不将 messages.sequence 回填为 parent。

懒初始化不产生可观察的部分提交：初始化 head、node 插入、head CAS 是同一事务。并发初始化通过 head 主键和后续 CAS 处理。

### 6.3 Head CAS

```sql
UPDATE conversation_entry_heads
SET active_leaf_id = ?, version = version + 1, updated_at = ?
WHERE conversation_id = ?
  AND version = ?
  AND active_leaf_id IS ?;
```

SQLite `IS ?` 让 nil→NULL 的比较可用。参数依次为 new leaf、UTC time、conversation、**调用者 expected version**、expected leaf。RowsAffected 必须为 1，否则返回 Conflict；整个事务 rollback。

append index 在同一事务内通过 `MAX(append_index)+1` 分配，来自整棵会话树，不来自 active path。移回历史节点不会重复 index。Depth 由实际 parent 决定。二者仅辅助存储和损坏检测，不替代 ParentID。

## 7. 事务和查询算法

### 7.1 Append

```text
调用前：冻结 Message 副本 → 结构/安全校验 → codec 编码 → 大小检查

WithinTx：
  1. tx 上校验 Conversation 存在
  2. 读取/懒初始化 Head；验证 expected leaf + expected version
  3. 读取 parent 与所属路径；校验无循环/跨会话/损坏
  4. 验证 pending tool group 与新 Message 的关系
  5. 检查新路径长度 newDepth+1 <= MaxPathEntries，防止提交后无法读取；分配新 Entry ID、append index、depth、UTC 时间
  6. INSERT node，parent = expected leaf
  7. UPDATE head CAS；影响行数 != 1 → Conflict
  8. callback 返回 nil，由 WithinTx 执行 COMMIT

只有 COMMIT 成功才返回 AppendEntryResult；否则返回零结果和分类错误。
```

未来的完成事件/SSE 必须在 Commit 成功后发布。Phase 1 本身不广播，不打开新的模型流，不改旧 sink。

重要：仓库 DB.MaxOpenConns=1。事务中必须用 `tx.Query/Exec`，不能调用内部重新从 db 获取连接的 GetHead/GetPath，否则可能等待自己的唯一连接。复用 `loadHeadTx/readPathTx` 私有函数。

### 7.2 多工具调用组校验

假设 A1 声明 c1/c2，当前 leaf 是 R1：

```text
A1(c1,c2) → R1(c1)
```

新结果 R2：

1. 从 parent 向前回溯连续 toolResult，恢复为声明顺序；
2. 找到紧邻这组结果的 assistant A1，不到更早历史中任意搜索 call ID；
3. 对已有结果校验 CallEntryID=A1、名字/ID/次序匹配且不重复；
4. 新结果必须对应下一个未完成调用 c2；
5. 重复 c1、错名、错 CallEntryID、跨分支 call、提前 A2 或 user 全部拒绝；
6. 全部工具结果完成后，下一 assistant/user 可继续。

树允许 A1 后暂时缺结果，因为工具仍在执行；ReadPath 可以返回这条事实路径。它不能直接被当作合法模型请求，最终 provider 协议检查属于 Projection 阶段。

### 7.3 MoveHead 和合法分支点

事务内验证 ExpectedHead，校验目标归属和路径，再 CAS 移动；节点不变。

```text
旧：U1 → A1 → U2 → A2
              ↑
移动到 A1 后：U1 → A1 → U3 → A3
                     └→ U2 → A2 （旧分支保留）
```

目标路径若结束在未闭合工具组中，MoveHead 拒绝；否则用户会得到一个缺结果且无法继续普通对话的分支。可选择组前的 user/assistant 或组结束后的最后 toolResult。nil 目标是显式 reset，不是 NotFound。

Phase 1 没有 Run 记录，不能靠此模块判断实际模型是否仍在执行。生产请求接入后，AgentRun 编排必须在 Run 活跃时禁止外部 MoveHead；这是后续接入门禁，不能把简单树 CAS 当作整个 Run 的锁。

### 7.4 读取路径

ReadPath 在短的只读事务内确定目标 leaf，并读取完整路径：

- 可取所有会话节点一次建立 byID 索引后回溯；首版优先实现 bounded parent 回溯，按 id 逐步读取；
- 每步维护 visited ID，检查所属会话、depth 递减、parent 实际存在；
- root depth=0，不能循环或跳出会话；
- 超过 MaxPathEntries 返回 Capacity，不返回貌似完整的截断历史；
- 反转为 root-to-leaf，提交读取事务后返回深拷贝。

初始性能为 O(path length) 次查询；单连接下长路径会占用连接。Phase 1 设有界限制并测量，若瓶颈明显再用 recursive CTE 批量读取；接口、数据正确性和完整返回语义不变。

### 7.5 Payload codec

DB column `kind/payload_version` 是唯一 envelope 版本来源，JSON 不重复保存可能冲突的 kind/schemaVersion。

普通 assistant：

```json
{
  "role": "assistant",
  "stopReason": "stop",
  "content": [{"type": "text", "text": "# 完整 Markdown 报告\n\n正文……"}]
}
```

工具 assistant：

```json
{
  "role": "assistant",
  "stopReason": "toolUse",
  "content": [
    {"type": "text", "text": "先检索资料。"},
    {"type": "toolCall", "toolCall": {
      "id": "call-1", "name": "search_question_memory",
      "policyVersion": "v1",
      "fields": [{"name": "querySummary", "value": "二叉树学习资料"}]
    }}
  ]
}
```

独立结果：

```json
{
  "role": "toolResult",
  "toolResult": {
    "callEntryId": "e_assistant_1",
    "callId": "call-1", "toolName": "search_question_memory",
    "isError": false,
    "result": {
      "policyVersion": "v1",
      "fields": [{"name": "matchCount", "value": "3"}],
      "preview": "找到 3 条资料", "truncated": false
    }
  }
}
```

Codec 要求：Encode 前验证 Safe 值和结构；Decode v1 拒绝未知字段、重复字段、不匹配 union 和尾随 JSON；校验长度、角色、policy identity。普通 `encoding/json` 解码并不自动拒绝重复 key，必须显式检测，或者在实现 review 中明确缩小该承诺。不支持的版本返回 UnsupportedVersion，不默认为文本。读损坏数据不修改原节点；不得把原始 JSON 打进错误日志。

## 8. 流式与历史的并存方式

Phase 1 不把 Entry 当 streaming 存储对象，也不新增 Draft 表：

```text
当前线上：LLM → callback → bufferedTextSink
                         → AppendAssistantText(messages)
                         → MemoryGenerationHub → SSE

新增设施：完整 EntryMessage → EntryTree.Append → SQLite nodes/head
                       （暂不挂到上述链路）
```

因此历史接口仍返回现有 Message，不能在此阶段返回“部分工具参数 Entry”。以后接入时，消息收敛后追加正式 Entry；运行中可变快照由现有流式模块或轻量运行状态承载，需另行 review。

用户已选择暂不做可靠事件重放。后续重连只保证同锁注册 subscriber 和复制内存快照，先快照再增量；这不保证慢客户端队列永不溢出。不存在 `after=seq` 数据库重放承诺。

## 9. 代码文件计划

| 拟新增文件 | 职责 |
| --- | --- |
| `backend/internal/domain/conversation/entry.go` | Entry/head/role/part 类型，不更改旧 Message |
| `backend/internal/domain/conversation/entry_store.go` | EntryTree port、输入输出、错误契约 |
| `backend/internal/domain/conversation/entry_tool_projection.go` | 工具策略、安全封装、codec 快照访问 |
| `backend/internal/domain/conversation/entry_validation.go` | 单消息和连续工具组校验 |
| 同目录 `entry*_test.go` | domain/projection tests |
| `backend/internal/infrastructure/repository/sqlite/entry_tree_repository.go` | Append/MoveHead/GetHead 与事务 |
| `backend/internal/infrastructure/repository/sqlite/entry_tree_codec.go` | 唯一 JSON wire format |
| `backend/internal/infrastructure/repository/sqlite/entry_tree_path.go` | 路径读取、内部 tx helper、children |
| `backend/internal/infrastructure/repository/sqlite/sql_test/entry_tree_test.go` | 使用既有 newSQLTestDB 的集成测试 |
| `backend/internal/infrastructure/storage/migrations/<next>_conversation_entry_tree.sql` | 两张表、FK、不可变 trigger |

不改 server 装配/ChatTurn/Executor/前端。迁移会被现有 embed runner 应用；EntryTreeRepository 只有测试和未来接入点调用。只维护一个 EntryTree port，不把每个简单 SQL 再包装成一层 service。

## 10. 自顶向下实施切片

0. **开工前门禁**：先同步 OpenSpec 的 Phase 1 范围与本文，完成范围 review 和代码修改确认；这不是留到实现完成后才处理的任务。
1. **契约骨架**：定稿 EntryTree interface、类型、错误及 test fixtures；仅可编译，不接聊天。
2. **安全 payload**：实现文本/调用/结果校验、工具白名单投影、版本化 codec；验证敏感字段与大 Markdown block。
3. **Schema**：新增 migration，验证空库/旧库、composite FK、cascade、immutability。
4. **基础追加**：实现 lazy head、GetHead、Append、version CAS 和 rollback。
5. **路径与分支**：ReadPath、children、MoveHead、工具组上下文校验及 corruption 检测。
6. **Review gate**：测试、并发故障注入、旧 API 回归；复核已同步规范的一致性后交接后续 Run/Projection。

每层完成先汇报并等待确认，不因为文档列出下一步就自动修改其余模块。具体业务算法的实施分工在开工前对齐。

## 11. 测试设计与命令

### 11.1 Domain / codec

- user/assistant/toolResult 三角色、非法 role 和 tagged union；
- text/toolCall/text 的原始 block 次序，5000 delta 聚合出的长文本仍为一个指定 text block（流式聚合本身不在本阶段实现）；
- 重复 call ID、结果 CallEntryID/名字/ID 错误、错序、已完成 call 再次返回；
- pending group 可以保存，但新 user/assistant 不可跨过；
- 只投影白名单字段，不含 nested raw object、Authorization、Cookie、secret、原始错误；
- 截断不切断 UTF-8，policyVersion 稳定，编码回读无内容漂移；
- nil vs empty、未知版本、重复 JSON key、非法编码、超限 payload；
- 修改输入 slice 或读结果不改变下一次 repository read。

### 11.2 SQLite / 事务

- 空旧会话、新会话均可 lazy init；有 nodes 无 head 返回 Corrupt；
- root/child 路径与 depth/append index；同一会话多个 root 和 branches；
- 同旧 HeadToken 两 goroutine 只有一个成功，失败请求无残留 node/head；
- ABA：head 从 P→Q→P 后旧 P token 冲突；
- composite FK 拒绝 cross-conversation parent/head，不能只依赖 Go 校验；
- 故障注入 INSERT 成功后 CAS/COMMIT 失败，node 与 head 全部回滚；
- 禁止直接 UPDATE node、单独 DELETE 叶和祖先；DELETE Conversation 可 cascade 清除 heads/nodes；
- 错误 ToolResult 不推进 head；完整 c1/c2 按次序可落树；
- MoveHead 目标在未闭合工具组中拒绝；完整组末端可继续；
- 任意非当前 leaf 的历史路径也可读取；空/缺失/循环链不返回部分“成功”；
- 路径达到 MaxPathEntries 后拒绝继续追加，node/head 不变；边界内提交的路径可完整读回；
- 旧 messages、幂等键、现有聊天接口仍可读写，generation tables 不被重构。

### 11.3 验证命令

从仓库根目录运行（Go module 位于 backend）：

```bash
cd backend
go test ./internal/domain/conversation/...
go test ./internal/infrastructure/repository/sqlite/sql_test/...
go test ./internal/infrastructure/storage/...
go test -race ./internal/domain/conversation/... ./internal/infrastructure/repository/sqlite/sql_test/...
go test ./...
```

race 工具链若不可用需记录原因，不能写成已通过。本轮只写文档，不运行真实 migration，不宣称业务测试已完成。不需要启动前端或 LLM。

### 11.4 初次设计稿 SQL 校验记录

以下保留最初撰写本文时的检查记录：当时未实现领域代码或 migration，只对本文 SQL 做了临时内存库 smoke check。该历史记录不替代后续 Go 实现和回归测试：

- 使用本机 Node `node:sqlite`，SQLite 3.53.3；没有连接项目数据库；
- 验证了跨会话 parent/head 引用被拒绝、节点 UPDATE 和单节点 DELETE 被拒绝；
- 验证了 stale CAS 后 node INSERT 可一起回滚、ABA version 检查、nil head reset/append；
- 验证了多层、多分支的 Conversation 删除可同时 cascade 删除 nodes/head，foreign_key_check 无错误。

该结果仅验证 SQL 草案的相关行为，**不替代仓库锁定的 modernc.org/sqlite v1.58.0 集成测试**，也不代表 Go repository、codec、工具策略或业务回归已通过。

## 12. 观测、失败和回滚

- 记录有限错误分类、Entry ID、head version、节点/字节数量；不打印 payload、tool 原文或 secrets。
- expected token 失效返回 Conflict；不自动改挂新 parent，不无限重试。
- schema/codec/path 损坏返回分类错误；不忽略坏节点继续组装历史。
- 会话删除由原 ConversationStore.Delete 触发 cascade，不公开树节点删除能力。
- Phase 1 没有双写，因此 Entry 提交失败不会污染旧聊天写模型；旧表继续运行。
- 不修改 0001..0007；已发布 migration 不重写 checksum。
- 新 schema 一旦应用，旧版本程序可能因 max schema version 检查拒绝启动；**保留旧业务路径不等于可以直接回滚旧二进制**。回滚前必须备份数据库并评审版本兼容，优先 forward fix，不使用 DROP TABLE 清掉已写数据。

## 13. 方案取舍

| 选项 | 本阶段选择与理由 |
| --- | --- |
| 扩展旧 Message 为万能类型 | 不采用；会把可变 streaming、UI 与树事实混在一起 |
| 每个 delta 一条 node/part | 不采用；传输频率不是语义结构 |
| 不可变 Entry + Draft 表 | 本阶段只做 Entry；不引入尚无调用方的草稿表 |
| 具名多 head branches | 不采用；单 active leaf 足够，节点仍可共享祖先 |
| version CAS vs leaf-only CAS | 使用 version+leaf，防 ABA；不是 SSE revision |
| 无限 parent 回溯 | 使用 bounded 完整返回/明确超限，不静默截断 |
| RESTRICT parent/head FK | 使用 deferred NO ACTION + 删除策略，保留会话级删除 |
| Generic raw JSON 工具保存 | 使用工具级白名单和封闭安全类型，不声称完整可执行重放 |

## 14. 交付与 Review 清单

Phase 1 完成标准：

- [ ] Go 类型没有重定义旧 Message/MessageRole，不依赖 provider SDK；
- [ ] 正式节点只追加，流式 delta 不改节点；
- [ ] ToolCall 在 assistant blocks 内、ToolResult 独立 Entry；
- [ ] 安全投影从首个工具节点就有效，不留原文旁路；
- [ ] 同会话 composite FK、version CAS、ABA 和事务 rollback 通过；
- [ ] 分支/路径完整，损坏/超限明确失败；
- [ ] 不可变策略与整体会话删除同时成立；
- [ ] 旧聊天、SSE、messages 无功能改变；
- [ ] 未声称已接入 Run/Projection/Compaction/Draft/event replay；
- [ ] 更新 OpenSpec 阶段范围并完成用户 review，再分层批准代码实现。

### 后续接入的 Review 检查点

Phase 1 已提供随机 Entry ID、独立 head、版本 CAS、lazy head、bounded parent 回溯和安全投影；它仍不是生产聊天写入来源。

1. 单 Entry 1 MiB、路径10000条是当前默认保护值，运行接入时仍需容量验收。
2. 具体生产工具允许字段及策略版本在 Phase 2 接线前审计。
3. head reset 到 nil 是内部基础能力，不代表前端分支导航已开放。
4. 安全工具历史不能恢复完整原始参数，Phase 3 必须明确背景事实映射或分类错误。
5. system prompt 原文、thinking、artifact 和 compaction不属于 Phase 1 已交付能力。

Phase 2 按独立技术文档实施；不得把基础设施能力等同于已完成运行接入或模型历史切换。
