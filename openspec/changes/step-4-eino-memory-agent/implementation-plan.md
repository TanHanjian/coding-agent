# Step 4 分步实现计划

## 总体目标

把 Step 4 拆成 8 个可独立验证的小步骤。每完成一步，先运行对应测试，再进入下一步。

## 第 1 步：确认现有接口和 Eino 能力

目标：明确当前代码的扩展点，不写业务功能。

工作内容：

- 检查 `chat.Runtime`、`RuntimeBuilder`、`Executor` 的职责。
- 确认当前 Eino 版本的 Tool Calling、`ToolCall`、`ToolMessage` 和工具注册 API。
- 确认题目、作答、复盘 service 与 repository 的现有接口。
- 确认现有流式聊天协议是否支持结构化事件。
- 补充 Step 4 的接口设计说明。

完成标准：明确 Runtime 是否负责工具循环、Builder 如何注册工具、工具调用结果如何回传模型，且现有测试全部通过。

## 第 1 步核对结果

- 当前 `chat.Runtime` 只返回 Eino `StreamReader[*schema.Message]`，`Executor` 会拒绝带 `ToolCalls` 的消息。
- `interview.Builder` 已接收 `model.ToolCallingChatModel` 和 `tool.BaseTool`，但当前 Graph 仍是单次 ChatModel 节点。
- Eino v0.9.19 提供 `flow/agent/react`、`model.WithTools`、`compose.ToolsNode` 和 `schema.ToolMessage`，可用于实现受限工具循环。
- `question.Service.Search` 可用于题目搜索，`question.Service.Get` 返回题目、作答、复盘和附件上下文。
- `answer.Service.List`、`review.Service.List` 可用于独立历史读取。
- 当前 HTTP 流式协议只发送文本增量；结构化 Agent 事件需要在后续步骤扩展。

第 1 步完成。

## 第 2 步：实现受限 Tool Calling 循环

目标：完成“模型 → 工具 → 模型”的闭环。

工作内容：

- 扩展 `chat.Runtime`，支持工具配置或工具循环。
- 修改 Eino Builder，把工具注册到 ChatModel。
- 修改 Executor，识别 ToolCall。
- 执行工具并生成 ToolMessage。
- 将 ToolMessage 送回模型继续生成。
- 设置最大工具循环次数为 6。
- 工具调用期间只转发普通助手文本。

完成标准：模型无工具调用时行为不变；单次工具调用能继续生成；达到上限后安全失败；工具参数不出现在前端文本和日志中。

测试：普通文本流、单次工具调用、多轮工具调用、达到上限、工具错误和取消。

## 第 3 步：实现题目搜索工具

目标：让 Agent 能按用户问题搜索题库。

工作内容：

- 创建题目工具包，放在应用层或 Agent 适配层。
- 定义 `search_questions` 输入结构。
- 支持关键词、题型、难度、标签、作答结果和分页限制。
- 调用现有 `question.Service.Search`。
- 将领域结果转换为稳定的工具输出。
- 限制结果数量和正文长度。
- 对空结果返回空列表。
- 对内部错误返回脱敏工具错误。

完成标准：工具不直接依赖 SQLite；参数非法、空结果和 service 错误均有明确处理；结果包含题目 ID、标题、类型、难度和标签。

## 第 4 步：实现题目上下文和历史工具

目标：让 Agent 能读取某道题的完整复盘上下文。

工作内容：

- 实现 `get_question_context`。
- 实现 `list_question_attempts`。
- 实现 `list_question_reviews`。
- 复用现有 `question.Service.Get` 或领域读取接口。
- 返回题目、作答和复盘的来源 ID。
- 限制 Markdown 正文长度。
- 保持空作答、空复盘可正常返回。

完成标准：题目不存在时返回明确结果；作答和复盘按稳定顺序返回；不暴露 repository 或 SQL 错误；Agent 能使用上下文继续回答。

## 第 5 步：实现记忆候选状态

目标：让 Agent 能提出待确认的长期记忆，但不直接保存。

工作内容：

- 新增 `MemoryCandidate`。
- 新增 `MemoryCandidateStatus`。
- 新增 `PendingMemoryCandidate`。
- 定义候选类型：question、answer、review。
- 定义候选 ID、关联题目 ID、标题、正文、来源和创建时间。
- 实现按 conversation ID 保存和读取候选。
- 每个会话最多保留一个候选。
- 新候选替换旧候选前生成提示信息。
- 候选首版只保存在进程内。

完成标准：创建候选不会写入题目、作答或复盘表；同一会话最多一个 pending 候选；不同会话隔离；候选可被下一轮聊天读取。

## 第 6 步：实现聊天确认和领域写入

目标：用户明确确认后，才把候选保存到长期记忆。

工作内容：

- 定义确认文本识别规则。
- 支持明确确认、明确拒绝和含糊回复。
- 按候选类型调用题目、作答或复盘 service。
- 保存成功或拒绝后清除候选。
- 含糊回复保留候选并继续询问。
- 保存失败后清除候选并返回脱敏错误。
- 不允许 Agent 直接操作 repository。

完成标准：只有明确确认才写入数据库；拒绝和含糊回复不写入；保存失败不返回成功；不出现部分保存后仍报告成功的情况。

## 第 7 步：增加流式结构化事件

目标：前端能够知道候选状态和保存结果，同时保留原有文本流兼容性。

工作内容：

- 保留现有文本增量事件。
- 增加 `memory_candidate_pending`、`memory_saved` 和 `memory_save_failed`。
- 定义事件字段和 JSON 编码方式。
- 工具调用过程不发送给前端。
- 普通文本客户端继续正常显示回答。
- 事件中不返回敏感信息或完整内部错误。

完成标准：候选出现、确认保存和保存失败时前端都能收到对应事件，且原有聊天客户端不崩溃。

## 第 8 步：接入完整 Agent 并验收

目标：把前面的模块接成完整工作流。

工作内容：

- 在 Agent Builder 中注册四个题库工具。
- 更新系统提示词：优先使用题库材料，区分事实、推断和建议，不编造内容，保存前必须请求确认。
- 将候选状态接入聊天请求处理。
- 将 Agent 事件接入现有 HTTP 流式接口。
- 删除或替换旧的“ToolCalls 不支持”逻辑。
- 更新 Step 4 tasks 和验收记录。

完整验收场景：检索题目并回答；检索题目、作答和复盘并诊断；提出候选并确认保存；拒绝或含糊回复不保存；工具失败、循环过多和取消均安全结束；日志和输出不包含敏感信息。

## 最终验证

- `go test ./...`
- `npm run build`
- `git diff --check`
- 将 Step 4 tasks 中完成项全部勾选。
- 记录未实现的非目标：模型配置、向量索引、FSRS、checkpoint 和 Tool 审计。


