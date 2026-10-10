# Phase 3 技术设计：Context Projection 与模型历史切换

> 状态：已实现并完成本轮验收。所属变更：[refactor-conversation-message-tree](../openspec/changes/refactor-conversation-message-tree/design.md)。
> 前置：[Phase 1](phase1-entry-tree-technical-design.md)、[Phase 2](phase2-agent-run-technical-design.md)。用户已批准本阶段代码、契约修订与整组安全事实文本降级。

## 1. 范围

Phase 3 将生产模型历史切换为来源可追溯的 Entry Tree Projection，通过现有 ContextManager 组装。保留旧 messages 为可见 read model，HTTP/SSE、前端、Run 写入和当前内存工具循环不变。

不实现 compaction、摘要协调、token/window 预算、历史回填、公开分支、artifact、system/thinking/media 投影或原始工具协议恢复。超长路径返回现有容量错误，不静默裁剪路径。工具组文本的有界降级不等于历史压缩。

## 2. 数据流与模块

```text
Service -> Request(RunID, persisted UserMessage, legacy History)
  -> Executor -> ContextManager.Prepare
    -> ProjectionReader.ReadProjection (one SQLite snapshot)
    -> ProjectSnapshot (provider-neutral ContextMessage + provenance)
  -> Eino text adapter -> RuntimeInput.History
```

- `domain/conversation/context_projection.go` 定义 ProjectionQuery、ProjectionSnapshot、ContextMessage、ProjectionSource、ProjectionReader。
- `sqlite/context_projection_store.go` 在 AgentRunRepository 实现只读 port，复用路径和 Run 校验，不写 head。
- `agent/context/projection.go` 负责纯转换与校验，不依赖 Eino/SQLite。
- `agent/context/manager.go` 统一调用 reader；生产装配注入，不让 service/executor 自行选择来源。
- `application/chat/context.go` 传递 RunID 和投影历史，保留旧 History 仅供未切换的受控测试/评估路径。
- `agent/eino/executor.go` 将投影历史映射为 user/assistant 文本，不制造历史 ToolCalls。

## 3. 快照与当前 user

ReadProjection 输入绑定 ConversationID、RunID、UserMessageID。一个读取事务中加载 head、Run、真实 Message、父链及 legacy 水位。

生产初次 Prepare 仅允许 pending/running Run，LastEntryID 必须等于 UserEntryID，head 必须匹配 Run 的 leaf/version。读取 root 到该 user 的完整路径，验证 user/BaseEntryID 及 Run 归属，再排除末尾当前 user；历史严格截止本轮 BaseEntryID，当前 user 通过 Query 单独进入模型一次。并发 head 变化、缺 parent、环路、跨会话、未知版本、超容量均分类失败，无部分模型历史输出。

LastClosedEntryID 属于 Run，不属于 head。正常失败由 Phase 2 回退 head；Projection 不另取闭合前缀掩盖 pending path，不修复 head。历史路径必须完整闭合。失败分支不在当前父链内，不能混入历史。

## 4. Legacy 水位

Phase 2 已对旧会话的新请求创建树，因此不能用“有 head 则全部丢弃旧 Message”分流。

水位为该会话最早 tree-backed Run 关联的真实 user Message.sequence，命名 LegacyBeforeSequence。读取水位前 completed user/assistant 作为 legacy 文本，再拼接当前树路径后缀；水位后双写 Message 不重复纳入。水位由既有 Run/Message 关联确定，不用时间戳或 sequence 猜 parent，无新增数据库 schema。验证边界关联；缺失边界或树化后出现无 Run 覆盖的 legacy 正文返回明确错误，不全量 fallback。

纯 legacy 的受控入口必须绑定真实 UserMessageID；只有没有非空树时读取该 user 前历史。旧 client id 重试仍仅复用，不创建 Run/Entry 或重新执行。每条 legacy 来源使用真实 MessageID，EntryID 留空。

## 5. 已批准转换策略

版本：`context-v1`；工具降级策略：`safe-tool-facts-v1`。

普通 user/assistant 合并有序 text parts，保留 EntryID、payload version 和 projection version。未知角色、block、payload 或安全策略必须拒绝。

assistant 含 toolCall 时，先校验完整原始路径及整个工具组：声明与结果身份、名称、数量、次序、错误标记和白名单。缺失、重复、孤立、pending 或未知策略不可通过文本降级掩盖。

每个闭合工具组统一转换为一条 assistant 历史事实文本，保留原 assistant 文本及全部调用/结果的声明顺序。结构化字段用 JSON 编码，明确为安全投影而非原始参数/完整结果；包含安全 fields、preview、IsError、Truncated。不生成 tool 角色、ToolCalls 或可执行 arguments。单组最大 64 KiB，全部源校验后进行 UTF-8 安全截断，显式标记组截断并保留全部来源 metadata。

每条 ContextMessage.Sources 保留真实 assistant/result EntryID、payload/projection/policy version、call ID、call Entry ID、tool name、error/truncation。组级 DowngradePolicy 记录批准决策。转换后再次验证消息角色/来源，模型适配器保留来源 metadata 于内部 schema 消息，不输出到 HTTP/SSE 或日志。不得用日志或 artifact 取回 raw payload。

## 6. 接线与未来边界

ContextManager 注入 ProjectionReader 后，在 legacy fallback 或未来 policy 编排前读取并转换投影。当前 policy 管线尚未实现完整来源保留，若与 Projection 同时启用则明确拒绝，不静默丢来源。未注入 reader 的受控评估/单测继续旧 pass-through；生产 reader 缺失不得成为新 Run 的静默 legacy fallback。

PreparedContext 携带独立 ModelHistory 和安全计数/水位诊断；ModelHistory 的非 nil 空 slice 表示已投影的空历史，不能回退 req.History。Eino 在 Build/模型调用前验证并转换；历史工具文本不自动触发工具执行，当前 Run 的真实内存协议不受此策略替换。

## 7. 实施与验收

1. 契约与 provenance 类型、批准策略。
2. SQLite 同快照读取、Run 绑定、legacy 水位。
3. 原始路径/工具组校验及纯文本转换。
4. ContextManager、Eino、server 生产装配。
5. 定向/全量测试、vet、独立 review 与证据记录。

验收覆盖空历史、普通文本、多工具闭合组、安全字段/来源/UTF-8 截断、pending/重复/孤立拒绝、缺 parent/跨会话/未知版本/容量、失败分支隔离、新旧混合去重、当前 user 一次、head 冲突、生产接线与 HTTP/SSE 回归。

OpenSpec CLI 不可用时仅记录手工契约核对，不宣称 CLI validate 通过。性能目标不能替代实际测量，本阶段不宣称未经测量的 <10ms。

## 8. 验收记录

本轮实现与验收完成，证据如下：

- `go -C backend test -count=1 ./...`：后端全量通过，包含 storage/migration、HTTP/SSE 与旧聊天回归。
- `go -C backend vet ./...`：通过。
- `context/projection_test.go`：多调用/错误事实、安全字段过滤、来源拷贝、64 KiB UTF-8 截断、pending/孤立/重复/错名/未知版本与策略/跨会话/父链/不支持 block 拒绝。
- `sqlite/context_projection_store_test.go`：空树/纯 legacy/混合历史、水位去重、当前 user 排除、只读无写入、Run/user 绑定、uncovered legacy 拒绝、head/version 冲突、损坏节点与容量分类、失败分支隔离。首次树化 Run failed/cancelled 后仍保留 legacy 前缀及闭合工具事实，排除之后废弃的未闭合组。
- 同一测试文件通过两个独立数据库连接，在读取 head 后并发提交 Run 终态，验证当前事务仍读取完整旧 snapshot，后续读取看到终态并拒绝重新准备。
- `context/manager_projection_test.go`、`eino/executor_projection_test.go`、`chat/context_projection_adapter_test.go`：reader 绑定、异常不 fallback、非 nil 空历史、模型边界拒绝无来源/无效版本、metadata 深拷贝、历史无可执行工具协议。
- `interview/builder_projection_test.go`：直接 prompt 变量与真实 Graph→模型测试，验证 Sources、降级和截断 metadata 保留、当前 user 只出现一次、不读取被替代的 legacy History。
- `cmd/server/main_test.go`：有效模型配置下缺失 reader 明确失败。
- 独立只读 review 覆盖 reader/converter/adapter/manager/executor/builder/server 与测试，未发现可确认的 Phase 3 实质 bug。已补充其指出的真实 Graph、首次树化失败/取消、闭合事实保留及 reader 必填关键测试；复核时顺便修正 prompt builder 对被替代 legacy History 的提前校验。

环境限制：race 命令因 `CGO_ENABLED=0` 返回 `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`，当前 Windows 工具链无 gcc，未运行 race。OpenSpec CLI 不可用，仅完成本阶段技术设计与 change artifacts 的手工契约核对，不宣称 CLI validate 通过。未执行真实外部模型调用或性能 benchmark。

Phase 4、历史回填与旧路径删除均未授权，不随 Phase 3 标记完成。工作区既有 Phase 2 改动保持原样，本记录不替代其生产切换/恢复/所有权等独立验收。
