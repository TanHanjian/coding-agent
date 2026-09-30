# Entry Tree Specification

## Requirements

### Requirement: [MVP] Append-only conversation entry tree

系统 MUST 将会话上下文事实和需要参与投影的会话状态保存为带稳定 `id`、`parentId`、`conversationId`、类型、payload version 和创建时间的 Entry。除 root 外，每个 Entry MUST 指向同一会话中已存在的父 Entry；Entry 创建后 MUST NOT 原地修改 payload、父指针或创建时间。

#### Scenario: Append child to active leaf

- **WHEN** 服务使用当前 active leaf 作为 expected parent 追加合法 Entry
- **THEN** 系统创建唯一 Entry，并将 active leaf 原子移动到新 Entry
- **AND THEN** 新 Entry 的 parentId 等于追加前的 active leaf

#### Scenario: Reject stale parent

- **WHEN** 两个请求使用同一个旧 active leaf 追加，而第一个请求已经成功推进 leaf
- **THEN** 第二个请求 MUST 返回明确冲突
- **AND THEN** 系统 MUST NOT 创建孤立 Entry 或覆盖第一个请求的 active leaf

### Requirement: [MVP] Active-leaf branch navigation

系统 MUST 支持将 active leaf 移动到当前树中已有的 Entry，以便下一次追加从该 Entry 创建新分支。分支操作 MUST 保留原有 Entry，不复制、不修改、不删除旧路径；第一阶段 MUST 不要求具名 Branch 记录。

#### Scenario: Create alternate branch

- **WHEN** 用户选择历史 Entry 作为新的 active leaf并提交新的用户消息
- **THEN** 新消息 MUST 以所选 Entry 为 parentId
- **AND THEN** 原 active leaf 所在路径仍可从树中读取

#### Scenario: Reject entry from another conversation

- **WHEN** 请求尝试把其他会话的 Entry 设置为当前会话 active leaf
- **THEN** 系统 MUST 拒绝操作并保持原 active leaf 不变

### Requirement: [MVP] Provider-neutral message facts

系统 MUST 将 system、user、assistant 和 toolResult 消息以 provider-neutral payload 保存，并将 assistant tool call 与 tool result 通过稳定 `toolCallId` 配对。assistant 内容 MUST 能表达文本、thinking、tool call 和必要的媒体引用；tool result MUST 能表达有界内容、错误标记和安全详情。

#### Scenario: Multiple tool calls

- **WHEN** 一个 assistant 消息声明多个 tool call，工具并行完成
- **THEN** 系统 MUST 为每个 tool result 保留独立 Entry、相同的 toolCallId 关联，并按声明顺序形成 active path
- **AND THEN** 不得把一个结果错误关联到另一个 tool call

#### Scenario: Orphan tool result

- **WHEN** 请求追加没有对应 assistant tool call 的 tool result
- **THEN** 系统 MUST 拒绝该 Entry
- **AND THEN** 不得改变 active leaf

### Requirement: [Phase 2] Tool-level safe persistence projection

系统 MUST 只将工具白名单声明的字段和有界内容写入持久化 Entry。未声明字段、凭据、原始内部错误、完整大 payload 和内部推理 MUST NOT 进入消息树、普通日志、诊断或 SSE；超出上限的结果 MUST 使用有界预览和受限 artifact 引用表示。

#### Scenario: Unapproved field

- **WHEN** 工具投影包含未在当前版本策略中声明的字段
- **THEN** 系统 MUST 丢弃或拒绝该字段
- **AND THEN** 其余安全字段仍按策略处理且不得泄露原始对象

#### Scenario: Artifact expired

- **WHEN** 模型或服务读取已经超过 TTL 的 artifact
- **THEN** 系统 MUST 返回明确的 expired 状态
- **AND THEN** 不得把过期文件当作空结果或重新暴露原始内容

### Requirement: [Phase 2] Persist compaction as an entry

系统 MUST 将成功提交的 compaction 保存为新的树 Entry，并保留原始被压缩 Entry。compaction MUST 记录摘要 schema/version、被替代范围的明确边界和必要的预算 metadata。

#### Scenario: Compaction commit

- **WHEN** 摘要、边界、消息协议和 active leaf CAS 均校验成功
- **THEN** 系统追加 compaction Entry 并推进 active leaf
- **AND THEN** 原始消息仍可通过历史树读取

#### Scenario: Compaction failure

- **WHEN** 摘要生成、校验、存储或 CAS 失败
- **THEN** 系统 MUST 不追加 compaction Entry、不推进覆盖边界、不修改原始 Entry
