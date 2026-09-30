# Entry Tree Specification

## Scope

需求标签为产品阶段。实施 Phase 1 已实现 message Entry Tree 与安全白名单基础，但未接入聊天；实施 Phase 2 接入 Run，仍不支持 system/thinking/media、compaction、artifact 或 Draft。后续能力不得以空壳类型或完成任务的形式提前声明。参见 [design](../../design.md) 与 [tasks](../../tasks.md)。

## Requirements

### Requirement: [MVP] Append-only conversation entry tree

系统 MUST 为已收敛的消息事实保存稳定 `id`、`conversationId`、`parentId`、kind、payload version 和创建时间。非根 Entry MUST 指向同会话中已存在的父 Entry；已创建 Entry 的 payload、parent、身份与时间 MUST NOT 原地修改或单独删除。append index 和 legacy sequence MUST NOT 替代 parent；整体会话删除 MUST 清理其树与 head。

#### Scenario: Append a child atomically

- **WHEN** 调用方使用有效的 expected head 追加合法消息
- **THEN** 系统 MUST 在同一事务创建 Entry，并将 head 移动到新 Entry
- **AND THEN** 新 Entry 的 parent MUST 等于操作前的 active leaf，COMMIT 失败 MUST 不发布成功结果或残留节点

#### Scenario: Reject a conflicting identity

- **WHEN** 插入与已有 Entry 身份或物理追加索引冲突，或尝试修改已有 payload
- **THEN** 系统 MUST 拒绝操作，不得通过 replace/upsert 覆盖历史
- **AND THEN** 普通 Entry append MUST NOT 被宣称为 client 请求幂等；该语义属于 AgentRun

### Requirement: [MVP] Leaf and version compare-and-swap

所有追加与 head 移动 MUST 同时校验调用方的 expected leaf 和 expected version，成功后 MUST 推进 version。系统 MUST 支持内部读取指定 leaf 的完整路径和移动到同会话闭合分支点或显式根前位置；旧路径 MUST 保留。实施 Phase 2 MUST NOT 开放公共分支操作，active Run 期间 MUST 禁止外部移动 head。

#### Scenario: Reject an ABA token

- **WHEN** head 从 P 移到 Q 再移回 P，而调用方仍提交第一次 P 的版本
- **THEN** 系统 MUST 返回明确冲突，不追加节点或覆盖新 head
- **AND THEN** 系统 MUST NOT 自动使用最新 version 或重新选择 parent

#### Scenario: Preserve an alternate path internally

- **WHEN** 无 active Run，内部操作以有效 token 移动到合法历史闭合点并追加消息
- **THEN** 新 Entry MUST 从该点形成新路径，旧分支仍可内部读取
- **AND THEN** 此基础能力 MUST NOT 被解释为已经提供用户分支 API

#### Scenario: Reject an unsafe target

- **WHEN** 目标属于其他会话、缺失或路径结束于未闭合工具组
- **THEN** 系统 MUST 拒绝移动并保持 head 不变
- **AND THEN** Run 的失败回退 MUST 只使用已验证闭合点并执行同样的 leaf+version CAS

### Requirement: [MVP] Bounded provider-neutral message facts

当前系统 MUST 只写入 `kind=message` 的 user、assistant、toolResult。user MUST 使用 text；assistant MUST 保留完整响应中有序的 text 和安全 toolCall blocks；toolResult MUST 使用独立 Entry 并保存 `callEntryId + callId + toolName`、安全结果与真实错误标记。流式 checkpoint、未收敛参数、内部 thinking、system prompt 原文和媒体 MUST NOT 写成当前版本 Entry。

#### Scenario: Persist multiple tool calls

- **WHEN** 完整 assistant 消息声明多个 tool call，工具以任意顺序完成
- **THEN** 每个安全结果 MUST 成为独立 Entry，并按 assistant 声明次序形成路径
- **AND THEN** 系统 MUST 保持调用身份、名称、内容块顺序和结果关联，不得把显示摘要当作完整消息

#### Scenario: Reject an unsupported fact

- **WHEN** 写入包含 system/thinking/media、未闭合参数、未知 kind 或不支持的 payload version
- **THEN** 系统 MUST 返回明确输入/版本错误，不得保存通用 raw JSON 旁路
- **AND THEN** Entry 和 head MUST 保持不变

### Requirement: [MVP] Validate pending and closed tool groups

路径 MUST 校验 assistant 声明与 toolResult 的身份、名称、数量和次序，同一路径 MUST NOT 重复 call ID。系统 MUST 允许完整 assistant call 形成暂时 pending 的路径，但 MUST NOT 允许新 user/assistant 越过未闭合组。工具组闭合与消息本身收敛 MUST 分开判断。

#### Scenario: Reject an orphan or out-of-order result

- **WHEN** 新结果没有当前 pending assistant 声明、关联错误、重复或不符合下一声明次序
- **THEN** 系统 MUST 拒绝该 Entry，head MUST 不变
- **AND THEN** 不能搜索不相关分支的调用来补配关系

#### Scenario: Read a pending group without fabricating results

- **WHEN** 路径已保存完整 assistant call，但部分结果尚未提交
- **THEN** 内部读取 MUST 保留已存在的事实并标识未闭合状态
- **AND THEN** 系统 MUST NOT 伪造 cancelled/aborted toolResult；模型使用和终态回退分别受 Projection/Run 契约约束

### Requirement: [MVP] Tool-level safe persistence projection

首个工具 Entry 起，系统 MUST 只持久化工具级版本白名单批准的有界标量字段与由这些字段生成的 preview。未知策略 MUST 默认拒绝；未声明字段、凭据、原始参数/结果、内部推理和原始错误堆栈 MUST NOT 进入 Entry、Run、旧 Message、普通日志、诊断、备份或 SSE。SafeToolCall MUST 标识为非可重放安全投影，不能作为原始 arguments 使用。

#### Scenario: Filter unapproved data

- **WHEN** 原始工具对象含未声明字段或敏感字段，或试图直接指定 raw preview
- **THEN** 系统 MUST 过滤/拒绝未批准内容，preview MUST 只由已批准字段派生
- **AND THEN** 不得将 raw 对象写入错误、显示事件或备用持久化字段

#### Scenario: Enforce bounds without artifacts

- **WHEN** 调用字段超限或结果超过安全字段/preview 上限
- **THEN** 调用投影 MUST 明确拒绝，结果 MUST 有界截断并标记 truncated，或返回明确容量错误
- **AND THEN** MVP MUST NOT 强制 artifact、保存完整原文或生成不可读回的 artifact ID

### Requirement: [MVP] Isolated complete path reads

所有 Entry/head/path 查询 MUST 受 conversation 归属约束；内部 active path 读取 MUST 使用一致快照并完整返回 root-to-leaf 顺序。损坏、循环、跨会话 parent、缺失 head/parent、未知版本或容量超限 MUST 返回可诊断错误，MUST NOT 返回貌似完整的截断路径或自动修复历史。

#### Scenario: Reject a cross-conversation reference

- **WHEN** 使用另一个会话的 Entry ID 进行读取、追加关联或 head 移动
- **THEN** 系统 MUST 拒绝或返回 scoped not found，不得泄漏其他会话 payload
- **AND THEN** parent/head 的持久化约束与事务校验 MUST 保持同会话隔离

#### Scenario: Detect corruption or capacity limits

- **WHEN** 读取遇到缺失 parent、非空树缺失 head、循环或超出完整路径容量
- **THEN** 系统 MUST 返回明确一致性/容量错误，不发送部分历史、不自动改 parent
- **AND THEN** 空且合法的会话 MUST 可返回逻辑空 head 和空路径

### Requirement: [Phase 2] Optional restricted artifacts

artifact/TTL MUST 作为核心 Run/Projection 之后独立可选的未来 capability，不属于实施 Phase 1 或 Phase 2 Run。仅当另行批准并启用时，系统 MUST 使用不透明 ID、conversation/run/tool call 归属、受限读取、TTL、原子发布和清理。artifact MUST NOT 成为永久 Entry 正文、raw tool/凭据/内部推理存储旁路，也 MUST NOT 是安全投影或 compaction 的强制前置。

#### Scenario: Artifact expires after optional enablement

- **WHEN** 已启用能力中的 artifact 超过 TTL
- **THEN** 读取 MUST 返回明确 expired，不得当作空结果或再次暴露过期内容
- **AND THEN** 永久 Entry 中的有界安全事实 MUST 不依赖 artifact 永久有效

#### Scenario: Artifact access crosses a conversation boundary

- **WHEN** 请求使用其他会话的 artifact ID 或越过读取额度
- **THEN** 系统 MUST 拒绝读取，不暴露物理路径或原始内容
- **AND THEN** 未启用该能力的 MVP MUST 仍可通过安全 preview/truncated 或容量错误工作

### Requirement: [Phase 2] Persist compaction as a future entry

在后续 compaction 实施阶段，系统 MUST 将校验成功的摘要保存为版本化 compaction Entry，并保留原始 Entry。覆盖范围 MUST 使用明确 Entry/path 边界、summary schema/version 和必要预算 metadata，不得用旧 sequence 替代来源。该能力 MUST NOT 被标记为实施 Phase 1/2 已完成。

#### Scenario: Commit a valid compaction

- **WHEN** 摘要 schema、来源边界、工具协议、预算和 active leaf+version CAS 均成功
- **THEN** 系统 MUST 原子追加 compaction Entry 并推进 head
- **AND THEN** 原始事实 MUST 仍可读取，摘要 MUST 不引入其他分支或未授权原文

#### Scenario: Reject a failed candidate

- **WHEN** 摘要生成、校验、存储或 CAS 失败
- **THEN** 系统 MUST 不追加 compaction、不推进覆盖边界、不修改原始 Entry
- **AND THEN** 失败 MUST 可诊断，不得假装已压缩
