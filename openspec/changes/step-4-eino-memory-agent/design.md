# Design

## Agent runtime

沿用现有 `chat.Executor`、`RuntimeBuilder` 和会话生成生命周期。Builder 组装 Eino Graph，Executor 负责消费模型流。模型产生 ToolCall 时，runtime 执行已注册工具并把 ToolMessage 送回模型，循环最多执行 6 轮。

一次用户请求由一个 Run 和一个或多个按顺序编号的 Model Step 组成。每次 `chat_model` 调用开始时创建新的 `stepId`。该调用产生的文本 delta 必须立即经由 SSE 发送，不得为了等待 ToolCall 而缓冲整轮输出；文本在 Run 结束前只是进行中的内容，不能被解释为该 Run 已完成。

当同一次 Step 在已产生文本后再产生 ToolCall，Graph MUST 继续消费该 Step 直到 ToolCall 参数完整，然后路由至 tools 节点；不得因为已有文本而提前 `END`，也不得以“文本后 ToolCall”为协议错误。工具结果回填为 `ToolMessage` 后，下一次 `chat_model` 调用创建新的 Step。最终没有 ToolCall 的 Step 输出正文并使 Run 正常结束。

## Tool boundary

工具只依赖题库领域 service，不直接依赖 SQLite。首版注册 `search_questions`、`get_question_context`、`list_question_attempts` 和 `list_question_reviews`。工具输出使用稳定结构，限制结果数量和正文长度，并保留来源 ID。

## Memory confirmation

Agent 运行时为每个 conversation 保留最多一个 `PendingMemoryCandidate`。候选包含 ID、类型、关联题目 ID、建议内容、来源、创建时间和 `pending` 状态。确认和拒绝通过下一轮聊天文本识别；只有明确确认才调用对应领域 service。含糊文本不会写入。确认、拒绝、过期或保存失败后清除候选。

## Persistence and errors

候选首版只存在进程内会话状态，不要求服务重启后恢复。普通聊天消息继续由现有 `TurnStore` 持久化。工具错误、模型错误、取消和超时沿用现有生成终态；错误响应只返回脱敏分类信息。完整工具入参、结果和逐 Step 执行轨迹不写入 SQLite；活跃 Run 的内存 Hub 仅保留重连所需的脱敏 Step 状态摘要。

生成中重启必须遵循本机单实例语义：Go 进程启动时独占本机数据目录；目录占用失败则拒绝第二实例启动。在接受任何新请求前，对持久化状态中遗留的 `streaming` Run/assistant message 执行原子收敛，终态改为 `failed`，错误码为 `generation_interrupted`，并保留已经持久化的可见文本。恢复逻辑不得以 Hub 缺失单独判定执行失效，也不实现 checkpoint 或从中间位置继续生成。

客户端重试使用 client id 幂等：相同 client id 复用并返回既有结果/终态，不重复执行；新的 client id 视为新的 Run 并重新提交。撤销或失败后的重提必须由调用方生成新的 client id。

## Trace and observability boundary

完整 Trace 默认关闭，并与脱敏 Trace 独立配置。配置开关不得代替用户授权。若后续接入可选平台，必须在上传前告知接收方、用途和内容范围，用户明确授权后才能上传范围内的完整真实学习内容；本地记录授权范围、授权时间和告知版本。撤销后停止后续完整上报，已上传内容不自动删除；接收方、用途或内容范围变化时重新授权。凭据永不上传，原始错误必须脱敏。普通日志、诊断信息和 UI 摘要不等同于授权 Trace，默认不包含完整题目、个人答案或原始 payload。该可选平台不成为本地 MVP 的运行依赖。

## Stream events

保留现有文本增量协议，并增加按 Step 有序的结构化 Agent 事件。每个事件必须关联稳定的 assistant `messageId` 与 `stepId`，顺序为：`step-start`，零或多个文本 delta / 工具输入事件，工具输出或错误事件，`step-finish`；整个 Run 最后发送原有完成、取消或失败终态。工具调用对活跃 SSE 订阅者可见：输入开始、由工具定义的可展示输入摘要、可展示输出摘要或脱敏错误均使用结构化 part，不能拼入 Markdown 文本。原始参数、原始结果和内部错误不得发送到前端、写入日志或持久化。

前端必须按 UI Message parts 的原始顺序渲染文本与工具活动，不能把工具卡片统一移动到文本之前。断线重连时先发送已有文本和 Step 摘要；不尝试从 SQLite 恢复完整工具卡片。普通历史消息仅恢复持久化的可见文本。
