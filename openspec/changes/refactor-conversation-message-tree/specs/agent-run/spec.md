# Agent Run Specification

## Requirements

### Requirement: [MVP] One AgentRun per client submission

系统 MUST 将一次新的用户提交表示为一个可追踪的 AgentRun，并以 `conversationId + clientMessageId` 提供幂等语义。相同 client id 的重复请求 MUST 返回既有 Run/结果，不得重复追加用户 Entry 或重复启动模型执行。

#### Scenario: Retry same client id

- **WHEN** 客户端以相同 conversationId 和 clientMessageId 重试请求
- **THEN** 系统 MUST 返回既有用户/Run/助手结果快照
- **AND THEN** 不得创建第二个用户 Entry 或第二个活跃执行

#### Scenario: Submit new client id

- **WHEN** 客户端使用新的 clientMessageId 提交
- **THEN** 系统 MUST 创建新的 AgentRun
- **AND THEN** 新 Run 的 base entry MUST 是提交时读取到的 active leaf

### Requirement: [MVP] Bounded Run lifecycle

AgentRun MUST 支持 `pending`、`running`、`waiting_tool`、`completed`、`failed` 和 `cancelled` 状态，并以原子状态转换收敛到唯一终态。Run MUST 保存入口/当前/最终 Entry 引用，但 MUST NOT 用一段大 JSON 保存完整模型历史或工具正文。

#### Scenario: Tool loop

- **WHEN** 模型返回带 tool call 的 assistant message
- **THEN** Run MUST 进入 waiting_tool 或等价的受控状态，工具结果完成后回到 running 并允许下一次模型调用
- **AND THEN** 工具调用次数超过上限时 Run MUST 以脱敏失败终态结束

#### Scenario: Terminal race

- **WHEN** 取消、模型完成和错误处理同时竞争同一个 Run
- **THEN** 只能有一个终态转换成功
- **AND THEN** 迟到事件不得修改最终状态或可见最终 Entry

### Requirement: [MVP] Single active Run per conversation

同一个 Conversation 在第一阶段 MUST 同时只允许一个 active Run。后到请求 MUST 返回明确 conflict/busy 结果，不得与现有 Run 交叉追加同一 active leaf；不同 Conversation 可以并行执行。

#### Scenario: Concurrent submissions

- **WHEN** 两个新的 client id 同时提交到同一 Conversation
- **THEN** 一个请求创建 active Run，另一个请求返回 conflict/busy
- **AND THEN** 不得产生交叉 parentId、重复 active leaf 或悬空用户 Entry

### Requirement: [MVP] Detached cancellation and restart convergence

显式取消 MUST 终止对应 Run，并保留已经安全持久化的可见内容。服务重启后，在接受新聊天请求前，系统 MUST 将遗留 active Run 和兼容 read model 的 streaming 状态收敛为 `failed/generation_interrupted`；系统 MUST NOT 从中间 checkpoint 恢复模型或工具执行。

#### Scenario: Cancel active run

- **WHEN** 用户取消仍在运行的 Run
- **THEN** Run MUST 进入 cancelled，已持久化内容保持不变
- **AND THEN** 迟到模型事件不得追加到已收敛的 Entry

#### Scenario: Restart during generation

- **WHEN** 服务在 Run 仍处于 running 或 waiting_tool 时重启
- **THEN** 新实例在接受请求前 MUST 原子标记该 Run 为 failed/generation_interrupted
- **AND THEN** 相同 client id 重试 MUST 返回既有失败结果，新 client id 才能创建新 Run

### Requirement: [MVP] Redacted ordered RunEvent stream

系统 MUST 为 Run 内模型 step、文本增量、tool call、工具状态和终态事件提供稳定顺序和 step identity。事件可供活跃订阅和受限重连使用，但 MUST NOT 包含原始工具参数、原始工具结果、凭据、系统提示词、内部推理或未脱敏错误堆栈。

#### Scenario: Reconnect active run

- **WHEN** 客户端在 Run 活跃期间重新订阅
- **THEN** 系统 MUST 先返回已持久化可见文本和脱敏 step 摘要，再发送后续事件
- **AND THEN** 不得从事件或旧消息伪造完整工具卡片

#### Scenario: Slow subscriber

- **WHEN** 订阅者无法及时消费事件
- **THEN** 模型执行 MUST NOT 被无限期阻塞
- **AND THEN** 订阅者可通过新的 snapshot 重新获得一致的可见状态
