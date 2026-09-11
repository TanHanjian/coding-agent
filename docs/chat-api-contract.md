# 聊天与流式接口契约

> 状态：待实现；本文是前端与后端的共同契约。  
> 适用范围：React 前端使用 Vercel AI SDK `useChat` 访问 Go 后端。  
> 不包含：Eino prompt、模型档案、Tool 策略、RAG 检索实现与断线续传。

## 1. 设计决定

- 持久化模型只包含 `Conversation` 和用户可见的 `Message`；Eino Trace、Turn 和 Tool 执行均是运行时概念，不落 SQLite。
- 前端使用 AI SDK UI Message Data Stream 协议，而不是 plain text stream。该协议可承载后续的引用、Tool 状态与完成事件。
- `POST /api/v1/chat` 是唯一触发模型执行的入口。已有 Message CRUD 仅用于历史管理，不得由前端用来伪造助手终态消息。
- 同一个 Conversation 同时最多只能有一条 `streaming` 的助手 Message；新的聊天请求必须收到冲突错误，不能并行写入同一会话。
- 首版支持同一进程内通过 `GET /api/v1/chat/{assistantMessageId}/stream` 重新订阅；服务端用 snapshot + delta 补齐当前可见文本。服务重启后不能继续同一上游请求，前端读取最后持久化内容并提供重新生成。

## 2. 会话与历史接口

现有的 REST 接口继续作为历史读取与管理契约：

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| `POST` | `/api/v1/conversations` | 创建 Conversation。 |
| `GET` | `/api/v1/conversations` | 按 `updatedAt DESC, id DESC` 列出 Conversation。 |
| `GET` | `/api/v1/conversations/{conversationId}` | 读取 Conversation 元数据。 |
| `PATCH` | `/api/v1/conversations/{conversationId}` | 修改标题。 |
| `DELETE` | `/api/v1/conversations/{conversationId}` | 删除会话及全部 Message。 |
| `GET` | `/api/v1/conversations/{conversationId}/messages` | 按 `sequence ASC` 读取已持久化的可见 Message。 |
| `GET` | `/api/v1/messages/{messageId}` | 读取单条 Message 及其状态。 |

`GET .../messages` 的领域 DTO 由前端映射为 AI SDK `UIMessage`。首版只映射 `user`、`assistant` 与单个 text part；将来引入引用和 Tool UI 时才扩展为多 part。

## 3. 发起聊天

### `POST /api/v1/chat`

前端通过 `DefaultChatTransport.prepareSendMessagesRequest` 只发送 Conversation ID 和最新用户 UI Message；服务端从 SQLite 读取已持久化的历史，不接收客户端整段历史作为模型上下文来源。

请求：

```json
{
  "id": "c_01...",
  "message": {
    "id": "client-msg-01...",
    "role": "user",
    "parts": [{ "type": "text", "text": "解释单调栈的常见边界条件" }]
  }
}
```

请求规则：

- `id` 是已存在的 Conversation ID。
- `message.id` 是前端生成的幂等键；服务端必须持久化为 `client_message_id`，并对 `(conversation_id, client_message_id)` 建立唯一约束。
- `message.role` 必须为 `user`。
- 首版仅接受一个非空的 `text` part；附件、文件、数据 part 与 Tool part 均返回 `invalid_request`。
- 同一幂等键的重试不得创建重复 user Message 或 assistant Message；若已有未完成生成，返回其助手 Message ID 和当前状态。

成功后，服务端按以下顺序执行：

1. 原子创建 user Message 与空的 assistant Message（`status=streaming`）。
2. 将 assistant Message ID 注册到进程内 `ActiveGenerationRegistry`。
3. 调用后续注入的 `ChatExecutor`，批量持久化助手文本。
4. 以 AI SDK UI Message Data Stream 持续发送响应；结束时把 Message 标记为 `completed`、`cancelled` 或 `failed`。

响应头：

```text
Content-Type: text/event-stream; charset=utf-8
x-vercel-ai-ui-message-stream: v1
Cache-Control: no-cache, no-transform
X-Conversation-ID: c_01...
X-User-Message-ID: m_01...
X-Assistant-Message-ID: m_02...
```

响应体必须完全遵循 AI SDK UI Message Data Stream v1。服务端在开始时提供对应的助手消息 ID；文本以 text part 发送；正常完成发送 finish part。当前不发送 reasoning、Tool、file 或 source part，但编码器必须保留扩展能力。

请求在写入响应头前失败时，返回既有 JSON 错误体。响应已开始后发生失败时，必须发送 AI SDK error part，并将助手 Message 标记为 `failed`。

## 3.1 当前代码骨架

- `internal/transport/httpchat/handler.go` 负责 HTTP 路由、请求 DTO 校验、订阅与 AI SDK UI Message Stream v1 编码；`internal/application/chat/` 负责 Chat 用例编排和后台生成生命周期。
- `TurnStore` 固定了后续 SQLite 实现必须提供的原子消息对创建/幂等、文本追加和终态更新能力；`0004_chat_turns.sql` 已提供 `client_message_id` 与会话内唯一约束，普通 Message CRUD 不承担这些并发语义。
- `GenerationHub` 隔离 Eino 输出和 HTTP Data Stream 编码。只能在 `Start` 成功持久化消息对之后订阅并发送流响应；Eino Executor 不得直接操作 HTTP。
- 应用装配根创建 OpenAI ChatModel、面试 Graph Builder 和 Eino Executor，并把 Executor 注入 Chat Service；模型调用不进入 Handler。

## 3.2 生成运行与 SSE 订阅分离

- 浏览器的 `POST /api/v1/chat` 与 `GET /api/v1/chat/{assistantMessageId}/stream` 都只是订阅者；刷新页面只能断开旧 SSE，不能取消 Eino 或上游模型流。
- Chat Service 必须从服务级 `RunContextFactory` 创建生成上下文，不能把 HTTP 的 `request.Context()` 传给 `Executor.Stream`。显式取消、超时和服务关闭才是运行取消来源。
- 首版恢复使用 `snapshot + delta`：Hub 在同一把锁内注册订阅者并复制当前完整文本；HTTP 先发送 snapshot（前端替换全文），再发送后续 delta（前端追加），不需要 `afterSequence` 或事件回放。
- `messages.content` 仍批量持久化为刷新后加载历史的最后检查点。进程内 Hub 不得因慢 SSE 客户端阻塞 Eino；服务重启后无法继续同一上游模型请求，前端显示最后检查点并提供重新生成。
- 现有 `generation_events` 迁移为未来的跨进程精确回放预留，首版不写入也不读取它。

## 4. 取消生成

### `POST /api/v1/chat/{assistantMessageId}/cancel`

取消以助手 Message ID 为目标，而不是 Conversation ID；这避免未来允许多会话并发时取消错误的生成。

取消流程：

1. `ActiveGenerationRegistry` 原子取得该 ID 的 `context.CancelFunc`，确保取消与正常完成只有一个终态胜出。
2. 取消执行上下文，强制刷入内存中最后一段文本。
3. 将该助手 Message 更新为 `cancelled` 并移除运行时注册表项。
4. 返回最终持久化 Message。

响应规则：

| 情况 | 状态码 | 结果 |
| --- | --- | --- |
| 首次取消进行中的 Message | `200` | 返回 `status=cancelled` 与已累积内容。 |
| 重复取消已取消 Message | `200` | 返回同一最终 Message，幂等。 |
| Message 不存在 | `404` | `not_found`。 |
| Message 已完成或失败 | `409` | `generation_not_active`，响应包含当前 Message。 |

浏览器主动中断 `/chat` 响应只会结束该订阅；服务端继续运行并持久化生成结果。用户需要停止生成时，前端显式调用取消接口；同进程内可通过 stream 接口重新订阅。

## 5. 错误与前端状态

普通 JSON 错误沿用 `{ "error": { "code", "message", "requestId" } }`。聊天接口新增：

| code | HTTP | 前端动作 |
| --- | --- | --- |
| `generation_active` | `409` | 显示当前生成，禁用该会话的新发送。 |
| `generation_not_active` | `409` | 刷新该 Message；不显示为系统故障。 |
| `model_unavailable` | `503` | 保留用户输入，提供重试。 |
| `stream_failed` | 流内 error part | 保留助手已输出文本，显示失败状态。 |

前端根据持久化 Message 状态渲染：`streaming` 显示停止按钮；`cancelled` 显示“已停止生成”；`failed` 显示可重试错误；`completed` 为普通回答。取消控制器、ReadableStream 和 `ActiveGenerationRegistry` 都是运行时资源，不进入 Redux 或 SQLite。

## 6. 明确延后

- `POST .../regenerate`：需要先定义新助手 Message 与原用户 Message 的关联。
- 跨进程精确事件回放：需要 `agent_turn_events` 或兼容的字节流日志；当前只提供单进程 snapshot + delta 重订阅。
- Tool / citation / reasoning UI part：需要 Eino Tool 与 RAG 契约先定稿。
- 自动续跑：需要 checkpoint、幂等 Tool 和持久化 Run 状态。

## 7. 实现验收

- [ ] AI SDK `useChat` 可通过 `DefaultChatTransport` 与 `prepareSendMessagesRequest` 消费 `/api/v1/chat` 的 UI Message Data Stream。
- [ ] 重试同一 `message.id` 不产生重复持久化记录或第二个模型调用。
- [ ] 取消会停止 executor、持久化最后文本并返回相同助手 Message ID。
- [ ] 取消与完成竞争时仅有一个终态；终态不得回到 `streaming`。
- [ ] 浏览器重载通过 `GET .../messages` 恢复已完成、取消或失败的文本与状态。
- [ ] 流开始前使用 JSON 错误；流开始后使用 AI SDK error part，并且不泄露凭据或上游原始错误。
