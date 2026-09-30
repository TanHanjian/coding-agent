# Design

## 1. Scope and Delivery Map

本文落实已批准的规划，不授权本轮修改业务代码或 migration。产品阶段标签 `[MVP] / [Phase 2] / [Phase 3]` 与实施顺序 **Implementation Phase 1/2/3/...** 独立。

| Implementation phase | Product phase | Boundary |
|---|---|---|
| Phase 1 | MVP | 已实现独立 Entry Tree 代码/测试：message、user/assistant/toolResult、leaf+version CAS、安全白名单；未接 ChatTurn |
| Phase 2 | MVP | 待实现 AgentRun 生命周期、ChatTurn 原子集成、完整安全 assistant/toolResult 事实、取消/恢复；旧线性模型 history 和现有 HTTP/SSE 保留 |
| Phase 3 | MVP | 待实现 Context Projection 与模型适配器转换，才切换模型 history 来源 |
| Phase 4 | Phase 2 | 未来 compaction、预算、摘要提交/复用 |
| Optional follow-up | Phase 2 | 未来独立可选 artifact/TTL，在核心 Run/Projection 之后；不属于 Phase 1 或 Phase 2 Run |
| Phase 5 | MVP / Phase 2 | 按能力验收兼容、显式历史迁移与清理；核心双写已在 Phase 2 完成 |

详细实施 Phase 2 设计见 [AgentRun Technical Design](../../../docs/phase2-agent-run-technical-design.md)；基础实现背景见 [Entry Tree Technical Design](../../../docs/phase1-entry-tree-technical-design.md)。需求与验收分别见 [proposal](proposal.md)、[tasks](tasks.md)、[Entry Tree spec](specs/entry-tree/spec.md)、[AgentRun spec](specs/agent-run/spec.md)、[Projection spec](specs/context-projection/spec.md) 和未来的 [Compaction spec](specs/context-compaction/spec.md)。

## 2. Architecture and Dependencies

```text
Implementation Phase 2:
HTTP (clientMessageID) -> ChatTurn/Run coordinator -> one SQLite transaction
                                                -> Entry facts + Run refs + legacy messages/head
full-message control hook -> safe projection -> ordered fact commits
visible text checkpoint  -> legacy messages -> existing Hub/SSE (assistantMessageID)
model input              <- existing linear History + current-run in-memory tool loop

Implementation Phase 3:
Entry active path -> Context Projection -> provider-neutral AgentMessage -> model adapter
```

- Entry 是收敛后不可变消息事实，不是每个 delta 的存储对象。
- Run 是一次外部请求的身份、状态与引用，不是正文容器或模型上下文选择器。
- 旧 `messages` 是可变的可见文本/read model，保留现有预创建 streaming assistant 与 HTTP/SSE 寻址。
- 现有 Graph 显示回调、GenerationEvent、Hub 快照只服务展示；它们不构成完整 assistant/toolResult 事实，不进入 Projection。
- Projection 在实施 Phase 3 才成为新路径的模型上下文边界；新增树或 Run 表不意味着模型已经读树。

| Module id | Responsibility | Depends on |
|---|---|---|
| `entry-tree` | 不可变节点、head、路径/工具组校验、安全持久化投影、会话隔离 | — |
| `agent-run` | 请求幂等、事务编排、状态、取消、前置恢复、现有流兼容 | `entry-tree`、现有 ChatTurn/read model |
| `context-projection` | active path、provenance、历史事实映射、协议校验与适配器边界 | `entry-tree` |
| `context-compaction` | 最终预算、合法组边界、摘要 Entry、CAS 与复用 | `entry-tree`、`context-projection` |

实施顺序为基础 → Run 集成 → Projection → compaction；artifact 是核心 Run/Projection 之后独立可选能力，不再把安全工具投影延后到 artifact 阶段。

## 3. Entry and Safety Contracts

### 3.1 Supported Facts

实施 Phase 1/2 只支持 `kind=message`、`payloadVersion=1` 及 `user/assistant/toolResult`：user 为 text，assistant 为有序 text/toolCall，toolResult 通过 `callEntryId + callId + toolName` 关联 assistant 声明。每个结果独立 Entry；并行工具的落库顺序按 assistant 声明次序，不按完成先后。

节点保存稳定 ID、conversation、parent、payload version、安全 payload、append index、depth 和时间；创建后不得原地修改或单独删除。整体会话删除是独立数据生命周期操作，必须清理相关记录。append index/旧 sequence 不是 parent，也不决定模型 history。

system/thinking/media、compaction、branch_summary/context_edit 等不是当前可写空壳；未来类型需独立版本/schema/行为验收。运行期系统指令仍由可信 prompt 来源提供，内部推理和 system prompt 原文不落树。

### 3.2 Head Operations

现有基础 port 提供 GetHead/Get/ReadPath/ListChildren/Append/MoveHead。追加或移动必须比较调用方提供的 `expected leaf + expected version`，提交后递增 version，防止 ABA；不自动重新挂到最新 parent。节点插入与 head 推进同事务，COMMIT 失败不得返回成功结果。

MoveHead 只接受同会话、已校验且工具组闭合的目标或显式 nil 根前位置；旧路径保留。实施 Phase 2 不增加公共分支 API；活跃 Run 期间禁止外部移动 head。Run 的失败回退是受控内部操作，不是公开分支功能。

Phase 2 需要 tx-local Entry/Run/read-model helper，共享 ChatTurn 的同一个事务；不得在事务中调用另开事务/连接的公共 repository 方法再拼接“原子性”。

### 3.3 Mandatory Safe Tool Projection

[MVP] 首个工具事实即执行工具级、版本化白名单：只提取批准的有界字段，preview 只能由这些字段生成；未知策略、越权字段或不合法结构拒绝或过滤，不能回退保存原文。调用字段超限应拒绝；结果可有界截断并标记 `truncated`。工具字段值也必须逐工具隐私 review，字段名称白名单不是凭据安全证明。

原始参数/结果只在当前 Run 内存执行边界使用，不进入 Entry、Run、旧 Message、普通日志、诊断、备份或 SSE；错误保存受控分类，不保存原始堆栈。内部 thinking 和凭据同样禁止。工具安全字段仅是历史事实投影，不能还原原始 arguments，也不能自动用于执行或重放工具。

[Phase 2] artifact/TTL 为未来独立可选 capability，只有另行批准后才引入受限存储、归属、TTL、读取额度、原子发布和清理。MVP 超大结果返回有界 preview/truncated 或明确容量错误，不生成虚假 artifact ID、不要求保留完整结果。即使将来启用 artifact，也不能绕过隐私限制或提供普通 raw payload 旁路。

## 4. AgentRun Contracts

### 4.1 Identity, References and Invariants

拟议持久化字段（均为待实施 Phase 2 契约）：

| Field | Meaning |
|---|---|
| `id`, `conversation_id`, `client_message_id` | 请求身份；conversation+client id 唯一 |
| `user_message_id`, `assistant_message_id` | 现有 legacy Message 对的引用；HTTP/SSE 继续使用 assistantMessageID |
| `base_entry_id`, `base_head_version` | BeginTurn 创建新 user 前的 head 快照；base entry 可为 nil |
| `user_entry_id` | 本次请求的 user 事实 |
| `last_entry_id` | Run 最后已提交的事实，终态回退不改写此引用 |
| `last_closed_entry_id` | Run 最近已提交、路径无 pending tool group 的 Entry；初始为 user Entry |
| `final_entry_id` | 仅 completed 时存在，指向成功最终 assistant Entry |
| `head_version` | Run 最近一次 head 操作后的版本，包括失败回退后的版本 |
| `status`, safe error classification, timestamps | 生命周期和脱敏失败分类，无消息正文 |

active 状态为 `pending/running/waiting_tool`，终态为 `completed/failed/cancelled`。active Run 的 `LastEntryID/HeadVersion` 必须匹配当前 head；BaseEntryID/BaseHeadVersion 是不可变入口快照。终态回退后 LastEntryID 可以与 head 不同，HeadVersion 记录操作后的实际版本，不是最后事实的创建版本。FinalEntryID 只表示成功，不借失败部分文本填充。

Run 不保存 body、模型 history、raw tool JSON 或所谓恢复大对象。事实追加和新终态写入均核对 active status 和 head token；纯文本检查点只需对应 Run/Message 身份及状态门禁，不要求 delta 携带树 token。相同终态/legacy 的无写入复用必须先于 active/head 校验，迟到回调不能追加事实、文本或覆盖终态。

### 4.2 BeginTurn Transaction

顺序为 **幂等优先，busy 在后**：

1. 在同一事务内校验会话并查询 conversation+client id；有 Run 时核对关联关系并返回已有 Message/Run 快照，不执行。
2. 只有旧 Message 对而无 Run 时返回该旧对，`Reused=true`；不执行，不伪造 Run/user Entry，不因另一 active Run 而把幂等重试改成 busy。关联缺失/冲突返回一致性错误。
3. 新 client id 才检查 active Run 与 legacy streaming assistant，确认 head/path 合法且闭合；忙时零写入。
4. 读取 base token，追加 user Entry，创建旧 completed user + streaming assistant 对、pending Run 并推进 head；以上同一事务 all-or-nothing。初始 LastEntryID=LastClosedEntryID=UserEntryID，HeadVersion 为追加后的版本，FinalEntryID=nil。
5. 仅 COMMIT 成功才向 Start 返回新请求；模型继续使用原有线性 History，不提前切 Projection，不在重试中回填旧数据。

唯一约束与会话级串行共同防止并发 BeginTurn，不把普通 Entry Append 的冲突重试当作请求幂等。

### 4.3 Full-Message Control and Tool Batches

现有 Graph 显示回调可能只含摘要，且不能保证持久化错误阻断控制流。因此必须新增**可返回错误的完整消息控制 hook**：取得完整的本次模型输出，在工具执行前完成安全投影与 assistant call Entry 提交；取得完整工具结果，在下一次模型调用前完成配对、排序与持久化。保留来源实际提供的内容次序，不伪造适配器未提供的任意交错 block 顺序。显示事件仍只做展示。

assistant call 提交事务推进 head、LastEntryID/HeadVersion，并置 waiting_tool；有 pending group 时不推进 LastClosedEntryID。原始 arguments 仅用于本次内存工具执行。若事实提交失败，错误传播到执行器，不能继续工具或下一次模型调用。

完整工具结果批次按声明顺序构造多个独立 toolResult Entry，在一个事务内提交全部节点、head 和 Run 引用/状态。批次失败全部回滚；闭合后 LastClosedEntryID=LastEntryID，回到 running。真实工具失败可保存安全错误结果，但取消/缺失结果不能伪造成已执行结果。当前 Run 的完整内存消息循环仍供下一次模型调用，历史树还不供模型读取。

### 4.4 Checkpoints and Successful Completion

文本 checkpoint 只更新旧 streaming assistant Message，不创建/更新 Entry，不创建 Draft，不复制到 Run。先持久化文本再广播 delta；保存完整消息事实不能从 checkpoint 文本或 SSE 工具摘要反推。

无 tool call 的成功最终 assistant 由完整输出产生，并在 generation goroutine 收尾时经一次成功收尾事务提交：最终 assistant Entry + head + Run completed/LastEntryID/LastClosedEntryID/FinalEntryID/HeadVersion + legacy assistant completed。由现有 `FinishAssistant` 扩展承担该事务，不要求另建公开完成 API；其输入携带 Recorder 的 expected head 副本和完整 FinalMessage，新 active Run 收尾不能省略 token。启动失败补偿使用 BeginTurn 返回的 token，恢复使用已持久化 active Run 的 token，不从实际 head 刷新版本掩盖过期意图。最终文本先按既有机制 flush；事实 hook 不先单独插入最终 Entry 再做第二次终态事务。只有终态 COMMIT 成功才广播 terminal，旧可见文本不能被安全工具 payload 替换。相同终态重试复用既有快照，不重复追加；不同终态请求返回 conflict，不覆盖已有结果。

### 4.5 Failure, Cancellation and Pending-Group Rewind

失败/取消/启动恢复均按同一策略收敛：

- 当前路径闭合：保持 head 及所有已提交事实；Run/read model 原子转终态，FinalEntryID=nil。
- 当前路径存在 pending group：保留所有已提交的不可变 assistant call/部分结果所在分支；在终态事务中以 active Run 的 LastEntryID+HeadVersion 作 CAS，将 head 移到 LastClosedEntryID，并更新 Run.HeadVersion。
- LastEntryID 始终指向最后写入事实，不改成回退目标；LastClosedEntryID 必须是同会话且可验证的闭合祖先。初始 user 是合法闭合点，所以不把本次用户提交一并撤销。
- 不删除节点、不修改 parent、不增加 fake cancelled toolResult、不追加失败部分 assistant Entry。已 checkpoint 的可见文本保留在旧 Message。
- head 或引用不符合预期时事务失败，不能猜测新 parent、盲目回退或用当前版本覆盖旧 token。

执行上下文不继承浏览器请求取消，但必须继承进程 root 的关闭信号；服务关闭先停止新提交、取消/等待执行，再关闭 DB。显式 Cancel 只发出取消信号，generation goroutine 是已启动执行的唯一 finalizer：停止/隔离后续写入，使用独立短收尾上下文 flush 可见文本，再有序提交唯一终态。取消、成功与错误按已观察信号和终态提交顺序竞争；成功已提交不能被迟到取消覆盖，取消被 finalizer 观察后不能再完成成功。终态后 hook、checkpoint 和广播拒绝迟到写入。

Start 在 BeginTurn 提交后若 Hub/Registry 注册、pending→running 或启动准备失败且尚未启动 goroutine，必须补偿 Run 与旧 streaming Message 为 failed，按同样 head 策略处理并清理本次内存资源；不能留下 busy 槽位或假 streaming。执行只能在注册及 MarkRunning 成功后启动；Start 注册段与 Cancel 发送信号段须串行化，等待终态不持该锁，句柄缺失不能解释为中断。补偿失败必须显式报错，不能返回成功。此未启动分支与服务启动恢复不是 HTTP Cancel 的第二个 finalizer。

## 5. Recovery and Stream Compatibility

### 5.1 Startup Recovery

先取得单实例进程所有权，再执行恢复，成功后才开放服务；本设计不支持多个实例共享同一数据库运行生成，也不引入分布式租约/执行恢复。

恢复事务将遗留 pending/running/waiting_tool Run 及其 streaming read model 收敛为 `failed/generation_interrupted`，保留可见正文，并按闭合路径/pending group 策略处理 head。旧 streaming assistant 即使没有 Run 也须标记相同失败，不创建 Run/Entry，不移动没有可验证 Run 所属关系的 head。引用冲突、未知/不一致 head、CAS 或存储失败阻止启动；重复恢复应无额外事实或 head 移动。

服务中 Subscribe/Cancel 找不到 Hub/Registry 不能单独推断 generation_interrupted，不能偷偷执行启动恢复或改挂 head。运行中缺少内存句柄只返回持久化快照或明确运行时不可用错误；不能承诺恢复上游流。

### 5.2 Existing HTTP/SSE Contract

Start 的 conversation/user/assistant Message IDs、Subscribe/Cancel 的 assistantMessageID 和旧 Message 状态继续作为公共契约；RunID 是内部关联，不要求客户端迁移。寻址时校验 assistant role、Message/Run 的同会话归属和一致引用，ID 不是跨会话访问授权。

保留现有同锁订阅注册+内存快照、先快照再增量、有限 step 摘要和非阻塞慢订阅者策略；终态/重启后返回旧 Message 快照。不新建通用持久化 RunEvent 表，不以现有 `generation_events` 表存在来承诺数据库重放，不新增 after-sequence/revision/resync。慢客户端可能漏 delta，可重新读取快照；不承诺可靠事件投递或凭旧文本复原完整工具卡片。

## 6. Projection and Future Compaction

### 6.1 Implementation Phase 3 Projection

沿 active leaf 读取完整 root-to-leaf 路径，只包含当前路径；来源映射保留 Entry ID、版本、投影策略、call/result 关联和降级原因。缺 parent、跨会话、循环、未知版本或未闭合工具组不得发送部分“成功”历史。失败分支仍可内部审计，但不会自动混入新上下文。

SafeToolCall 字段不是原始函数 arguments：转换必须明确拒绝，或采用经批准、有界且保留 provenance 的历史事实文本降级；降级整组、明确安全摘要/截断含义，不制造 provider 工具执行承诺。只有具备另行批准且信息完备的合法协议映射才可保留工具协议，不能仅凭 ID/工具名和字段摘要承诺重建。不会从历史自动执行工具。

旧 user/assistant Message 只能作为标记为 legacy 的文本投影，缺失工具、thinking 或分支来源时不得伪造。新/旧混合路径须有显式迁移水位，避免重复当前 user、重复历史或无提示忽略旧历史。适配器保持独立，对不支持的结构明确失败/批准降级，不要求某个 SDK。

### 6.2 Product Phase 2 Compaction

压缩仍是未来能力，按 [Compaction spec](specs/context-compaction/spec.md) 在 Projection 后实现：按完整用户轮次和完整工具组选程序确定的前缀，保护当前请求/pending group；LLM 只总结选定安全内容。不能访问失败分支、raw tool/thinking/凭据，也不能因启用摘要放宽 source/隐私边界。

成功摘要以版本化 compaction Entry 保存明确的 Entry/path coverage、firstKeptEntryId 和预算 metadata；原始节点保留。摘要/schema/协议/最终预算或 leaf+version CAS 失败不提交、不推进覆盖边界。模型输入预算包含运行期 system/tool schema 等必要开销，但不因此持久化 system prompt。大结果先安全投影或明确容量失败；artifact 只有另行启用才可使用，并非压缩前置。

branch summary/context edit、system/media 新持久化类型及产品 Phase 3 长期记忆/完整执行恢复/具名分支不在当前 Run/基础范围，没有空壳 MUST 或提前完成声明。

## 7. Persistence, Indexes and Isolation

- 已有基础 `conversation_entry_nodes` / `conversation_entry_heads` 保存节点和 leaf/version；保持同会话 parent/head 约束与不可变保护，不改写已发布 migration。
- 待新增 `agent_runs` 只保存上述身份、状态、引用、脱敏错误和时间。对 `conversation_id + client_message_id` 建唯一约束；对 active 状态的 conversation 建部分唯一约束，保证单会话至多一个 pending/running/waiting_tool Run；assistant_message_id 提供唯一关联/查询索引。恢复查询使用 active 状态索引，其他索引按实际访问验证，不增加无调用方事件表。
- Run 的 Entry/legacy Message 引用均校验 conversation 归属，基础复合 FK 与事务校验共同保护；跨会话 Entry、client id 查询和 assistantMessageID 查询不能泄漏数据。client id 的作用域是 conversation，不是全库。
- BeginTurn、call 提交、结果 batch、成功 FinishAssistant、失败回退和 recovery 各自使用共享 DB 事务内的 tx-local helper；核心一致性不采用独立双写、异步 outbox 或“先返回成功后补写”。
- 会话删除必须同时清理 Entry/head/Run/legacy Message；新增引用不破坏级联删除。artifact 尚未启用，不增其表。现有 `generation_events` 和 `conversation_context_summaries` 不删除，也不被重新解释为 Run 正文或树摘要。
- migration 是后续代码审批事项；不重写已有 migration checksum，不把保留旧读取路径误称为可直接回滚旧二进制。

## 8. Migration Matrix

| Implementation phase | Fact/write path | Visible/stream path | Model history | Failure boundary |
|---|---|---|---|---|
| Phase 1 | 独立 Entry repository，未接业务 | 原 ChatTurn/messages/Hub | 原线性 History/旧摘要 | 基础事务失败不改变旧聊天 |
| Phase 2 | 新请求 Run+Entry+legacy 对原子写；旧重试只复用 | messages checkpoint/终态，assistantMessageID SSE | 原线性 History/旧摘要 + 本 Run 内存工具循环 | 新请求/事实/终态失败不能部分提交或虚假成功 |
| Phase 3 | 保留 Phase 2 事务；显式 legacy 映射/迁移 | 原 read model，不伪造工具卡片 | Projection；legacy 文本兼容须标记来源/水位 | 不合法协议明确拒绝/批准事实降级，不静默退回绕过安全检查 |
| Phase 4 | 新 compaction Entry | 原 read model | 验证后的 summary+合法后缀 | 候选失败不推进边界；旧 covered_sequence 不伪装树边界 |
| Phase 5 | 验收后独立批准迁移/清理 | 保留兼容合同 | 已验收的 Projection | 回填失败可诊断，不在幂等重试伪造历史 |

## 9. Risks and Verification Gates

| Risk | Mitigation / evidence |
|---|---|
| 把已有基础误报为完整 MVP | 仅基础任务保持 checked；Phase 2/3、compaction/artifact 全部待实现 |
| ChatTurn/Entry/Run 部分成功 | 共享事务与 COMMIT 故障注入；BeginTurn/成功收尾/read model 均验证 rollback |
| 取消或 pending group 阻塞后续请求 | 单 finalizer、终态有序、CAS 回退闭合点、保留 LastEntryID；无 fake result |
| 图摘要漏掉调用/持久化错误被吞 | 完整消息控制 hook；call commit 先于工具、batch commit 先于下一模型 |
| 安全摘要被当作原参数 | Phase 3 明确拒绝/批准事实文本降级；保留 provenance；禁止自动重放 |
| 重启误修未知 head 或 legacy orphan | 单实例前置恢复、关联一致性校验、失败阻止服务，不依据 Hub 缺失判失败 |
| 隐私旁路或 artifact 扩 scope | 工具逐项策略 review、Entry/Run/messages/log/SSE 静态与动态断言，artifact 独立可选 |

验收分基础、Run、Projection、未来 compaction/可选 artifact 和兼容清理进行。Phase 2 必测新/旧幂等、幂等优先于 busy、并发提交、声明顺序 batch、提交失败阻断执行、注册补偿、成功原子终态、取消竞争、未闭合回退/闭合保持、恢复 orphan/未知 head、会话隔离和现有 HTTP/SSE。基础代码和测试存在不代表本轮运行了测试；OpenSpec CLI 不可用，本轮只能手工核对文档、链接和任务状态。
