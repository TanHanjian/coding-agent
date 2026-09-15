# Guided Solving

## Requirements

### Requirement: Run bounded tool calling
Agent MUST 支持模型、工具和模型之间的 Tool Calling 循环，并限制单次请求的工具循环次数。

#### Scenario: Tool call returns useful context
- **WHEN** 模型调用已注册题库工具且工具成功返回
- **THEN** 工具结果被送回模型，活跃流按发生顺序发送工具活动和后续助手文本

#### Scenario: Tool call follows streamed text in the same model step
- **WHEN** 同一次模型调用已发送一个或多个文本 delta，之后才给出 ToolCall
- **THEN** 系统 MUST 立即保留并发送已产生的文本，不得等待整轮输出；并在 ToolCall 参数完整后执行工具、将结果送回下一次模型调用
- **AND THEN** 系统不得因文本先出现而提前结束 Run 或把 ToolCall 视为协议错误

#### Scenario: Tool loop exceeds limit
- **WHEN** 单次请求达到最大工具循环次数
- **THEN** 生成以安全错误终止，且不输出原始工具参数、原始工具结果或内部过程

### Requirement: Stream ordered agent steps
Agent MUST 将一次 Run 中每次模型调用表示为独立、有序的 Step。前端可实时接收 Step 的文本 delta、工具调用与工具结果，但不得据此将早期文本误判为整个 Run 已完成。

#### Scenario: Render text and tool activity in arrival order
- **WHEN** 一个 Step 产生文本、ToolCall、工具输出，随后下一 Step 产生更多文本
- **THEN** 流 MUST 带稳定 `stepId`，并以原始发生顺序发送对应 part
- **AND THEN** 前端 MUST 按 part 顺序渲染文本与工具活动

#### Scenario: Show only presentation-safe tool details
- **WHEN** 工具调用或工具结果被发送给活跃前端订阅者
- **THEN** 事件 MUST 仅包含由该工具明确声明的可展示摘要和脱敏错误
- **AND THEN** 系统 MUST NOT 发送、记录或持久化原始工具参数、原始结果和内部错误

#### Scenario: Reconnect during an active run
- **WHEN** 浏览器在 Run 仍活跃时重新订阅
- **THEN** 系统 MUST 恢复已持久化或已广播的可见文本及脱敏 Step 摘要
- **AND THEN** 系统 MUST NOT 从持久化历史重建、泄露或伪造完整工具入参和结果

### Requirement: Read question memory through services
Agent MUST 通过领域 service 提供题目、作答和复盘检索，不得直接访问 SQLite。

#### Scenario: No matching memory
- **WHEN** 检索没有匹配记录
- **THEN** 工具返回明确空结果，Agent 说明资料不足且不编造内容

### Requirement: Retrieve relevant question memory
Agent MUST 能根据用户问题检索相关题目、历史作答和复盘信息，并保留来源 ID。

#### Scenario: Relevant memory found
- **WHEN** 用户询问与已有题目相关的问题
- **THEN** Agent 返回基于检索内容的回答，并标明相关题目上下文

### Requirement: Confirm memory writes
系统 MUST 在保存新的题目、作答或复盘记忆前要求用户明确确认，并且每个会话最多保留一个待确认候选。

#### Scenario: User confirms candidate
- **WHEN** 用户确认 Agent 提出的记忆候选
- **THEN** 系统保存候选并返回保存结果

#### Scenario: User rejects candidate
- **WHEN** 用户拒绝记忆候选
- **THEN** 系统不写入题库或记忆数据

#### Scenario: Ambiguous confirmation
- **WHEN** 用户回复既不是明确确认也不是明确拒绝
- **THEN** 系统继续请求明确选择，且不写入长期记忆

### Requirement: Persist confirmed candidate through domain services
系统 MUST 根据候选类型调用对应领域 service；保存失败时 MUST 清除候选并返回失败状态。

#### Scenario: Confirm review candidate
- **WHEN** 用户确认复盘候选
- **THEN** 系统通过复盘 service 保存，并返回保存成功事件

#### Scenario: Save fails
- **WHEN** 对应领域 service 保存失败
- **THEN** 系统不返回成功状态，不执行隐式重试，并返回脱敏错误
