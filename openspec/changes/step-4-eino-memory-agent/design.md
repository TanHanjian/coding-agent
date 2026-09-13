# Design

## Agent runtime

沿用现有 `chat.Executor`、`RuntimeBuilder` 和会话生成生命周期。Builder 组装 Eino Graph，Executor 负责消费模型流。模型产生 ToolCall 时，runtime 执行已注册工具并把 ToolMessage 送回模型，循环最多执行 6 轮；工具调用参数和内部过程不写入用户可见文本流。

## Tool boundary

工具只依赖题库领域 service，不直接依赖 SQLite。首版注册 `search_questions`、`get_question_context`、`list_question_attempts` 和 `list_question_reviews`。工具输出使用稳定结构，限制结果数量和正文长度，并保留来源 ID。

## Memory confirmation

Agent 运行时为每个 conversation 保留最多一个 `PendingMemoryCandidate`。候选包含 ID、类型、关联题目 ID、建议内容、来源、创建时间和 `pending` 状态。确认和拒绝通过下一轮聊天文本识别；只有明确确认才调用对应领域 service。含糊文本不会写入。确认、拒绝、过期或保存失败后清除候选。

## Persistence and errors

候选首版只存在进程内会话状态，不要求服务重启后恢复。普通聊天消息继续由现有 `TurnStore` 持久化。工具错误、模型错误、取消和超时沿用现有生成终态；错误响应只返回脱敏分类信息。

## Stream events

保留现有文本增量协议，并增加最小的结构化 Agent 事件表达候选待确认、保存成功和保存失败。工具调用事件不直接发送给前端。
