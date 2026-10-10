# 上下文工程设计与实施计划（Graph-only）

- 状态：Graph 预算防护方案与实施已获用户批准，代码与文档已落地；review 的 3 项发现已修复，修复后的全量及专项复验通过。
- 当前运行时：Eino v0.9.19 Graph Runtime 与 compose callbacks；后续上下文工程继续围绕 Graph，不以 ADK 迁移为前置。
- 现状：Entry Tree projection 与持久化 compaction 已投产；本次新增实际模型调用前的预算检查和受控拒绝。
- 本次范围：完整文本请求估算、Graph 每次模型调用检查、摘要调用检查、输出上限、终态与安全反馈。运行内缩减、artifact 和运行内摘要属于后续设计，尚未实施。
- 验证：全量 `go test ./...`、`go vet ./...`、核心包 10 次随机顺序回归、前端 TypeScript 与相关测试、OpenSpec strict 校验通过；竞态检测受当前 CGO 环境限制，生产 token 精度与端点兼容性另行校准。
- 关联：[Graph 预算技术设计](graph-model-budget-technical-design.md)、[CozeLoop 观测方案](cozeloop-integration-plan.md)、[平台技术设计](cozeloop-platform-integration-technical-design.md)。

## 1. 当前状态与问题

生产装配见 `backend/cmd/server/main.go` 的 `newChatExecutor`。ContextManager 已接入 ProjectionReader、预算策略、边界选择、摘要协调器和 SQLite CompactionAdapter；不再将默认构造器的历史透传行为描述为生产现状。

`AgentRunRepository.ReadProjection` 在同一事务快照中校验 conversation/run/user、active head 和闭合历史路径，排除当前用户 Entry，并将树接入之前的 legacy 可见历史纳入兼容投影。树接入后的历史以 Entry Tree 路径和安全工具事实投影为来源，不能描述为仅恢复 user/assistant 可见文本。

持久化摘要由 `kind=compaction` 的 Entry 承载。`conversation_context_summaries`、`ContextSummaryRecord.covered_sequence` 属于旧表或内部兼容表示，不是当前生产摘要的权威边界；树上的边界使用 Entry ID、路径和 head/version。sequence 仅用于 legacy 覆盖边界，不能推导树 parent 或 compaction cutoff。

Graph 工具循环仍累积 assistant tool calls/results。当前已在 `chat_model` 的 StatePreHandler 中对累计输入逐次检查，超限或计数返回错误时停止调用；该防护不会自动缩减消息。跨轮 compaction 和实际调用 guard 是两个边界：历史准备通过，不代表渲染后的 Prompt 或后续工具轮次一定可以调用模型。

## 2. 范围与数据生命周期

| 数据 | 当前用途与状态 | 生命周期与恢复 |
| --- | --- | --- |
| 可见聊天记录 | 已有用户会话记录 | 沿用 SQLite 规则，不因压缩删除；仅 legacy 区段按 sequence 兼容恢复 |
| Entry Tree 与安全工具事实 | 已投产的历史路径与 projection 来源 | 原始事实保留，按 active path 和协议边界恢复；不等于永久保存原始工具 payload |
| compaction Entry | 已投产的结构化摘要与边界 | SQLite 持久化，验证 FirstKeptEntryID 与当前路径，使用 CAS/head version |
| Graph 运行内消息 | 实际调用 guard 的检查对象 | 每 Run 独立累积，尚未接入运行内缩减或摘要 |
| 临时工具 artifact | 后续设计，已允许有限本地落盘 | 会话隔离、配额、TTL、受控读回；尚未实施 |
| 长期记忆 | 后续独立目标 | 定义提取、来源、更新、遗忘和有界检索；尚未接入第三方服务 |

本次不交付完整 Run checkpoint、永久原始工具档案、工具卡片完整恢复、第三方记忆服务或 ADK 迁移。

## 3. Graph 架构与职责

```text
SQLite Entry Tree + legacy 兼容历史 + 持久化 compaction
                        |
                        v
ContextManager：projection → 历史预算与合法边界 → 摘要/复用 → compaction 提交
                        |
                        v
Prompt 模板：可信指令 + 摘要数据 + 保留历史 + 当前请求/访谈材料
                        |
                        v
Graph chat_model StatePreHandler：累积消息 → 完整请求估算 → guard
                        |
           预算内       |       超限/计数错误
                        v
                 模型调用 / 现有 Run finalizer
                        |
                        v
工具执行 → Recorder 提交事实 → 回到逐次检查边界
```

server 将同一经验证的预算快照注入历史准备、Graph guard、摘要 invoker 和 OpenAI 客户端输出 cap。摘要调用检查完成构造的摘要 Prompt，不绑定或计入聊天工具 schema。live eval candidate 也接入 Graph guard；judge 的配置与调用独立，未纳入该聊天预算策略。

一次 Build 读取工具 Info 并冻结参数 schema；WithTools、工具节点和计数共享快照，避免动态 Info 导致绑定和检查不一致。guard 不保存会话消息，调用序号保存在各 Run 的 agentState。

## 4. 当前预算契约与精度限制

判定式为：

`estimated_input_tokens + output_limit_tokens + safety_margin_tokens <= context_window_tokens`

等于边界允许，超过边界拒绝；实现通过减法计算剩余输入容量以避免加法溢出。取消优先。首版拒绝时不自动重试、不截断请求、不额外调用摘要模型恢复。

四项预算配置均须显式填写：`OPENAI_CONTEXT_WINDOW_TOKENS`、`OPENAI_MAX_OUTPUT_TOKENS`、`OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS`、`OPENAI_OUTPUT_LIMIT_MODE`。配置名称与规则已确定，无生产默认值；实际部署的窗口、输出上限、余量数值及端点模式兼容性仍须确认。余量可显式为零；窗口和输出上限必须为正，输出加余量必须小于窗口。输出模式只能为 `max_tokens` 或 `max_completion_tokens`，客户端只发送所选的一项，数值与输出预留一致。没有模型时保留不启用聊天的路径；模型已配置但预算不完整时拒绝创建聊天运行时。

当前计数器为 `openai-text-json-bytes-v1`：文本按 UTF-8 字节长度，完整请求按消息和工具定义构造的 JSON 表示经 `json.Marshal` 后取字节长度。覆盖 role/content/name、reasoning_content、tool-call index/ID/函数/arguments、tool_call_id、工具名称/描述/完整参数 schema 和 JSON 结构；排除适配器不发送的 Message.Extra/工具本地元数据。不支持的多模态、未知角色、nil 消息等返回计数错误。

该 JSON 表示不是整个 provider HTTP 请求的逐字节复刻，也不是特定模型 tokenizer。字节估算可能较早拒绝中文、代码和大 JSON；provider 隐含开销、reasoning 计费与真实窗口需要结合安全余量、端点能力和 Usage 校准，不能宣称数学保证永不超窗或已获得成本/质量收益。历史准备仍有独立粗估与分区策略，不能用它替代最终 guard。

review 的三项发现均已关闭：schema/custom estimator panic 在计数与 schema 边界捕获并安全拒绝；快照任意 JSON 值使用空容器解码与 `UseNumber` 保留大数、保持深复制隔离；frozen 工具保留全部八种可实现接口组合。数字/隔离、错误正文和真实 ToolsNode 执行路径测试，以及修复后全量回归均通过。

## 5. 持久化 compaction 与后续压缩契约

当前 `EntryCompaction` 包含 `Schema`（structured-v1）、`FirstKeptEntryID`、`SourceBoundary`、`TokensBefore` 和 `Summary`；`CompactionSummary` 包含 version、goal、confirmedFacts、decisions、constraints、openQuestions，以及兼容 context 字段。进行中任务/未完成项尚无独立字段，后续 schema 演进需独立设计。

当前 Manager 读取 projection、选择前缀/保留后缀、生成或复用摘要，通过 CompactionAdapter 写入 Entry Tree。写入以 ExpectedLeaf/ExpectedVersion 校验；摘要生成失败不追加候选 compaction。当前 Manager 对结构化 conflict 有使用本轮未持久化摘要继续的路径，不能将所有竞争情况描述成“摘要已经提交”。旧摘要表的 covered_sequence 不代表树上的提交边界。

以下是后续 Graph 压缩的契约和验收目标，不是本次新增能力的完成声明：

1. 程序决定删除边界，LLM 只总结选定前缀。树历史按稳定 Entry ID 和来源选择；运行内按快照及完整工具组选择，不将消息数组下标写入持久化边界。
2. 保留最大连续近期后缀，不跳过放不下的组选更早的小组；保护可信指令、当前用户请求和未完成工具组。
3. assistant 的多个 tool calls 与匹配结果整体处理，拒绝孤立、重复、缺失响应；摘要不能插入调用及响应之间。缩减结果正文仍须保留角色、ID 与协议结构。
4. 摘要来源、实际移除前缀和 SourceBoundary 必须一致；验证路径、结构、预算后提交。原始记录保留，候选失败不得发布新边界。
5. 过长摘要源按合法边界分批，重试有界；中间摘要不提前发布最终边界。该分批能力仍需独立实现/验证。
6. 会话 compaction 与未来运行内摘要分离。运行内工具材料不直接写入旧 summary 表，也不自动成为持久化事实；长期保存需使用来源可追溯的安全投影。
7. 未来缩减优先处理可恢复的大工具材料，再处理旧结果与历史；重建实际输入后重新经过同一 guard。不可裁剪部分仍超限则明确失败。

## 6. 临时工具 artifact（后续，尚未实施）

已允许有限本地落盘；具体 TTL、容量和工具白名单仍待确定，不能据此宣称 artifact 已上线。

- 只允许经工具策略确认的大文本结果；元数据包含 artifact ID、conversation/run/tool-call ID、创建/过期时间、大小、类型和状态。
- 服务端生成物理路径，模型使用不透明 ID；写成功才提供有界预览、截断标记和读回入口。
- 读回绑定当前会话身份，限制分页/范围和单次字节数；不得仅凭模型提供的 conversation ID 授权。
- 防止路径穿越、符号链接逃逸、跨会话访问和 ID 碰撞；Windows 权限与路径需专项验证。
- 凭据不得落盘；内容不能自动进入日志、UI 或 Trace。无法可靠清除敏感信息的工具禁用落盘。
- 设置单文件、单会话、全局配额；采用原子发布、I/O 租约、过期拒绝、会话删除清理、启动扫描与周期清理。
- 磁盘满、写失败或过期明确反馈，不能伪造完整可读结果；读回仍受最终预算约束，避免无限卸载/读回循环。

后续实施需与既有“禁止原始工具 payload 通用永久持久化”约束协调。本次文档更新不修改 OpenSpec，不将受限 artifact 设计视为既有规范已同步。

## 7. ADK 能力调研（历史，未采用）

此前核查 Eino v0.9.19 的 ADK summarization/reduction，曾建议先迁移 ADK 再接运行内 middleware；该建议已被 Graph-only 决定替代。以下仅保留版本调研事实，不作为实施依赖：

| 能力 | v0.9.19 调研事实 |
| --- | --- |
| summarization | BeforeModelRewriteState 改写 Agent state，提供 Finalize、Callback、Summarize；默认 ContextTokens 为 160000，触发条件严格大于阈值 |
| reduction | 工具返回时截断、调用前 clear；默认 clear 为 160000，截断上限为 50000 字节，默认保留工具参数 |
| 文件卸载 | Backend 只要求 Write，不自动提供读回工具、身份校验或 TTL |
| 临时状态 | rewrite 影响当前 Agent 迭代，不等于跨轮持久化；流式缩减仍可能累计完整输出 |
| 适配边界 | TranscriptFilePath 只插入提示；Callback 不是提交通知；自定义 Finalize 接管最终消息；handler 不能直接注册为 compose callback |

默认字节估算与阈值不等于项目模型的精确计数。只设置消息数而让 ContextTokens 为零可能立即触发；多种 rewrite 按注册顺序执行。未来借鉴算法仍需在 Graph 边界实现并独立验证，不因此引入第二套运行内核。

源码：[summarization](https://github.com/cloudwego/eino/blob/v0.9.19/adk/middlewares/summarization/summarization.go)、[reduction](https://github.com/cloudwego/eino/blob/v0.9.19/adk/middlewares/reduction/reduction.go)、[handler](https://github.com/cloudwego/eino/blob/v0.9.19/adk/handler.go)、[filesystem](https://github.com/cloudwego/eino/blob/v0.9.19/adk/filesystem/backend.go)。

## 8. 失败、观测与回滚

预算超限与计数失败使用应用层稳定码 `context_budget_exceeded`、`context_counting_failed`，经现有 RunService/FinishAssistant 收尾。取消和持久化失败保持优先级；failed 不提交 FinalMessage/FinalEntryID，已持久化可见文本和闭合工具事实保留，开放组遵守既有 head/CAS 规则。终态提交失败不广播未提交终态。

generation snapshot/update、HTTP SSE 和历史消息使用安全分类；前端实时与刷新历史展示固定文案，不透传 provider 错误或原始材料。

guard 的本地 debug 日志记录阶段、调用序号、计数器版本、估算、窗口、输出预留、余量、判定原因和耗时，不记录正文、凭据或主机路径。后续压缩率、artifact 生命周期与质量指标是待补能力，不能列为现有观测。CozeLoop 是可选出口，不新增外部发送权限，也不参与预算判定。

关闭未来摘要/卸载策略仍须保留最终 guard；全量无检查透传不是安全回滚。摘要可失效后重建，不删除原始聊天；未来 artifact 停止写入后仍须履行既定清理策略。

## 9. 实施与验证状态

实现映射以当前 Graph 装配为准，详细契约与 A1–A16 见 [Graph 预算技术设计](graph-model-budget-technical-design.md)：

| 实现文件 | 职责 |
| --- | --- |
| `backend/internal/infrastructure/config/budget.go`、`backend/internal/infrastructure/config/config.go`、`backend/.env.example` | 四显式配置、完整校验、无生产预算默认值 |
| `backend/internal/agent/context/model_budget.go`、`backend/internal/agent/context/request_estimator.go` | 调用 guard、输入分区、JSON 字节 v1 计数、schema 快照及 recovery/数字修复 |
| `backend/internal/agent/interview/builder.go`、`backend/internal/agent/interview/frozen_tools.go` | Graph 逐次检查、绑定与计数共享快照；保留原工具执行接口 |
| `backend/cmd/server/main.go`、`backend/internal/infrastructure/agent/openai/model.go` | 同源预算、摘要 guard、唯一输出 cap |
| `backend/internal/application/chat/model_errors.go`、`backend/internal/application/chat/run_service.go`、`backend/internal/application/chat/generation.go`、`backend/internal/application/chat/generation_lease.go` | 安全分类、既有事务收尾、终态传播 |
| `backend/internal/transport/httpchat/handler.go`、`web/src/features/chat/types.ts`、`web/src/features/chat/components/ChatPane.tsx` | SSE 与刷新历史的固定安全反馈 |
| `backend/cmd/eval/main.go`、`backend/internal/eval/runner.go` | live candidate 接入 guard；judge 独立 |
| `backend/internal/infrastructure/repository/sqlite/context_projection_store.go`、`backend/internal/agent/context/manager_compaction.go`、`backend/internal/infrastructure/repository/sqlite/compaction_adapter.go` | 已投产的 projection 与 compaction 路径 |

| 工作 | 当前状态 | 剩余检查 |
| --- | --- | --- |
| Entry Tree projection、持久化 compaction | 已投产 | 持续核对边界、竞争、失败和摘要质量 |
| 完整文本请求估算、四显式 env、输出 cap | 已实施并验证 | provider 精度、真实端点模式兼容性 |
| Graph 逐次 guard、摘要 guard、live eval candidate guard | 已实施，调用边界 review 通过 | judge 独立；生产 Usage 校准另行进行 |
| 安全终态、SSE 与历史错误展示 | 已实施，事务/head 和反馈回归通过 | 持续保持原有终态优先级 |
| 全量 Go、前端与规范验证 | `go test ./...`、`go vet ./...`、TypeScript、相关测试与 strict 校验通过 | 当前 CGO 环境不支持 race；A1–A16 证据见详细技术稿 |
| Graph 运行内缩减/摘要、artifact | 后续，未实施 | 方案细化、实现和生命周期/协议测试 |
| 长期记忆 | 后续预留 | 独立定义来源、删除、冲突和检索预算 |
| review 修复与生产 Usage 校准 | review 三项已修复并复核；校准未完成 | 实际端点参数与 token 精度仍需部署验证 |

OpenSpec CLI 已通过 `npx @fission-ai/openspec@1.14.1` 恢复可用，`graph-model-budget-guard` strict 验证通过。该 change 保持未归档，便于用户 review。

用户已批准本次 Graph 预算实施及文档更新，无需再次以阶段授权为由阻塞。后续需确定 artifact TTL/配额（“24 小时、10 MiB / 100 MiB / 1 GiB”仍仅是讨论起点）、任务状态 schema、运行内缩减策略和质量基线；这些不阻塞已批准预算防护的落地。
