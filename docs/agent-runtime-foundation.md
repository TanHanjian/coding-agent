# Agent 会话持久化基础技术设计

> 状态：设计基线，尚未接入 Eino 执行循环。  
> 读者：后续实现 Eino Agent、SQLite 持久化与流式接口的开发者。  
> 本文只描述待实现的聊天持久化能力；现有题库表请参阅 [数据库梳理文档](database-overview.md)。

## 1. 目标与边界

首版只将用户可见的会话和消息持久化到 SQLite，使用户取消生成或重启应用后仍能看到已输出的内容和其完成状态。

```text
Conversation（持久化会话）
    └── Message #1 user       completed
    └── Message #2 assistant  cancelled / completed / failed
```

Eino 的 Trace、Turn、Tool 调用和内部事件属于运行时编排概念，不在首版落库。一个 Eino Turn 精确表示“一次模型调用及其触发的全部 Tool 执行”；一次用户提问可能经过多个 Turn，因此不应把 Turn 固化为业务会话模型。

首版不实现 Eino 主循环、模型配置、RAG、Tool、HTTP/SSE、Token delta、checkpoint 或工具审计。

## 2. 领域模型

| 模型 | 职责 |
| --- | --- |
| `Conversation` | 用户可恢复的一段聊天记录，包含标题和时间。 |
| `Message` | 会话内一条实际可见的 `user` 或 `assistant` 内容。 |
| `MessageStatus` | 助手消息的生成状态；用户消息固定为 `completed`。 |

`MessageStatus` 允许值：

- `streaming`：已创建助手消息，仍在接收输出；
- `completed`：正常完成；
- `cancelled`：用户取消，保留取消前已累积的内容；
- `failed`：调用失败，保留已累积的内容和脱敏错误摘要。

## 3. SQLite 数据设计（待实现）

### 3.1 `conversations`

| 字段 | 建议约束 | 说明 |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY NOT NULL` | Conversation UUID。 |
| `title` | `TEXT NOT NULL DEFAULT ''` | 用户可编辑标题；初始允许为空。 |
| `created_at` / `updated_at` | `TEXT NOT NULL` | RFC 3339 Nano UTC 时间。 |

建立 `idx_conversations_updated_at(updated_at DESC, id DESC)`，用于稳定列出最近会话。

### 3.2 `messages`

| 字段 | 建议约束 | 说明 |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY NOT NULL` | Message UUID。 |
| `conversation_id` | 外键引用 `conversations(id) ON DELETE CASCADE` | 所属会话。 |
| `sequence` | `INTEGER NOT NULL CHECK (sequence > 0)` | 同一会话内的稳定展示顺序。 |
| `role` | `user` 或 `assistant` | 可见消息角色。 |
| `content` | `TEXT NOT NULL DEFAULT ''` | 用户输入或助手已累积输出。 |
| `status` | `streaming`、`completed`、`cancelled`、`failed` | 生成状态；用户消息必须为 `completed`。 |
| `error_code` / `error_message` | `TEXT NOT NULL DEFAULT ''` | 仅助手失败消息可写入的脱敏错误摘要。 |
| `created_at` / `updated_at` | `TEXT NOT NULL` | 创建与最后累积时间。 |

必须建立 `UNIQUE(conversation_id, sequence)` 和 `idx_messages_conversation_sequence(conversation_id, sequence ASC)`。删除 Conversation 必须级联删除 Message。

## 4. 流式、取消与恢复

```text
创建 user Message（completed）
    → 创建 assistant Message（streaming，content 为空）
    → 批量累积 content
    → completed / cancelled / failed
```

流式内容先在内存累积，再按约 200–500ms 或固定字符量批量更新同一条助手 Message；取消、失败和正常结束时必须强制写入最后一段内容，再更新状态。不得逐 Token 写入 SQLite。

前端根据 Message 渲染：`cancelled` 显示已累积内容及“已停止生成”；`failed` 显示已累积内容及可恢复错误提示；空内容的已取消消息显示“已停止生成（未生成内容）”。会话重启后按 `sequence ASC` 恢复，无需恢复 Eino 运行状态。

### 4.1 异常退出后的终态收敛（待实施）

首版本机数据目录由单个服务实例独占。服务 MUST 先取得覆盖进程生命周期的独占锁，完成迁移，再在接受聊天请求前通过事务将遗留助手 `streaming` 更新为 `failed`，错误码为 `generation_interrupted`，错误提示使用受控文本。保留最后已提交的内容、消息 ID、顺序和客户端幂等键；不得尝试补回未持久化文本或恢复上游执行。已处于终态的消息不变，重复恢复幂等。

无法取得独占锁的第二实例 MUST 拒绝启动；恢复事务失败 MUST 阻止聊天入口就绪。不能仅凭 Hub 缺失就清理可能仍在运行的消息。多进程共享数据库需要另行设计执行者身份和租约，本版不支持。

旧客户端幂等键重试只返回原失败结果。重新提问使用新幂等键创建新消息对，保留旧记录，专用 regenerate 仍延后。具体接口和验收见 [聊天契约 §3.3](chat-api-contract.md#33-服务重启后的中断恢复待实施)。

## 5. 持久化边界

领域层拥有接口，SQLite 仅提供实现。Eino Runtime 依赖接口，不直接执行 SQL。

```go
type ConversationStore interface {
    Create(ctx context.Context, conversation Conversation) error
    GetByID(ctx context.Context, id string) (Conversation, error)
    List(ctx context.Context) ([]Conversation, error)
    UpdateTitle(ctx context.Context, id, title string, updatedAt time.Time) error
    Delete(ctx context.Context, id string) error
}

type MessageStore interface {
    Create(ctx context.Context, message Message) (Message, error)
    GetByID(ctx context.Context, id string) (Message, error)
    ListByConversation(ctx context.Context, conversationID string) ([]Message, error)
    UpdateAssistant(ctx context.Context, message Message) error
}
```

启动恢复由应用层调用独立的恢复存储接口，在 SQLite 事务中批量完成，不由 Eino 或 HTTP handler 直接执行 SQL；它只负责结束上一进程的遗留消息，不承担 checkpoint 续跑。上面的 CRUD 接口不是恢复操作的完整接口定义。

`Create` 必须在事务中分配会话内的下一个 Message 序号。`UpdateAssistant` 只允许更新助手消息，并且只允许 `streaming → completed | cancelled | failed`；终态消息不可再回到 `streaming`。

## 6. 隐私与后续扩展

- Message 与普通日志、诊断不得写入 API Key、Authorization 头、Cookie、完整上游响应、原始 provider 错误或内部推理。
- 可选 CozeLoop Trace 与本地 Message 存储分离。默认不采集完整内容，只有配置开启且用户对接收平台、用途、学习内容范围作出有效明确授权后才允许完整学习内容上报；授权不允许凭据、原始错误或内部推理进入 Trace。撤销阻止后续完整上报，已上传数据不会自动删除；详细授权规范见 [本地数据与设置](../openspec/changes/build-interview-memory-agent/specs/local-data-and-settings/spec.md)。
- 未确认的 Agent 内容不得写入题目、作答、复盘或 RAG 长期语料；Message 持久化只用于恢复聊天记录。
- 删除 Conversation 不得删除题目、作答、复盘、索引或模型档案。

需要 Tool 审计、引用回放或断线续传时，再增加不可变的 `agent_turn_events`。需要服务重启后续跑时，再设计 Eino checkpoint、工具幂等键和状态快照；不得仅凭一条 Message 尝试恢复模型执行。

## 7. 实现验收清单

- [ ] 迁移创建 `conversations` 和 `messages`，以及外键、状态约束和索引。
- [ ] Conversation 可创建、读取、稳定列出和删除；删除会级联清理 Message。
- [ ] Message 按 Conversation 和 sequence 稳定读取；并发创建不产生重复 sequence。
- [ ] 取消与失败保留已累积的助手内容，并以 Message status 恢复展示。
- [ ] 用户消息只能为 `completed`；终态助手消息不能回到 `streaming`。
- [ ] 生成中强制退出并重启后，遗留消息在接受请求前变为 `failed/generation_interrupted`，文本与身份不变；恢复重复执行幂等。
- [ ] 第二实例无法取得独占权时拒绝启动，恢复事务失败时不开放聊天入口。
- [ ] 旧幂等键不重跑，新幂等键可重新提问，无需 checkpoint 或专用 regenerate。
- [ ] repository 测试使用临时 SQLite；日志、错误和测试数据不包含真实凭据。
