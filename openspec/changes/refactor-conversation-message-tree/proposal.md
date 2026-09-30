# Proposal

## Why

当前聊天以线性 `conversation.Message`、`sequence` 和预创建的 streaming assistant 为中心，无法持久表达完整工具交互与分支事实。需要把不可变消息事实、一次请求的执行状态、客户端可见文本及模型上下文选择分开，同时保留现有 HTTP/SSE 和隐私边界。

本 change 建立 append-only `ConversationEntry` 树，以 `AgentRun` 管理请求生命周期，后续由 Context Projection 选择模型上下文。Entry 不是流式草稿，Run 不保存正文，显示事件不是消息事实。

## What Changes

- [MVP] 建立 `kind=message`、`user/assistant/toolResult` 的 Entry Tree、`activeLeafId + version` CAS、路径校验和工具级安全白名单基础。请求幂等属于 Run，不承诺幂等 Entry append。
- [MVP] 将 AgentRun 原子接入现有 ChatTurn：新请求的 user Entry、旧 Message 对、pending Run 和 head 同事务创建；成功的最终 assistant Entry、completed Run 和旧 assistant 终态同事务提交。完整 assistant 消息和独立工具结果只保存安全投影。
- [MVP] 支持单会话单 active Run、client id 幂等、取消、启动注册失败补偿及服务前置恢复。未闭合工具组失败时保留不可变分支，CAS 将 head 回退到 `LastClosedEntryID`；不伪造工具结果。
- [MVP] 在后续实施阶段引入 provenance-preserving Context Projection，替换旧线性模型 history；安全调用字段不能伪装成原始参数重放，转换必须明确拒绝或采用经批准的事实文本降级。
- [Phase 2] 在核心 Run/Projection 之后实现持久化 compaction、合法组边界、预算复检和摘要复用。安全投影从 MVP 起强制执行，不依赖压缩。
- [Phase 2] 独立可选的受限 artifact/TTL 能力，须另行验收；不属于 Entry 基础或本次实施 Phase 2 Run 范围，也不是超大工具结果的必需前置。
- [MVP] 保留旧 `messages` 为用户可见 read model；旧 client id 只有旧 Message 对而无 Run 时只复用，不执行、不伪造 Run/Entry 回填。

## Non-goals

- 不引入 Draft、通用持久化 RunEvent 表、SSE replay/resync/revision 协议或自动工具重放。
- 不持久化原始工具参数/结果、凭据、内部 thinking、system prompt 原文或 provider 原始响应；实施 Phase 1/2 不增加 system/thinking/media Entry。
- 不引入具名 Branch 表；实施 Phase 2 不开放公共分支操作，不允许活跃 Run 期间外部移动 head。
- 不恢复模型执行，不把 checkpoint 文本或图显示回调摘要当作完整消息事实。
- 不改前端视觉界面，不隐含要求迁移 Agent SDK，不删除旧消息或把旧 sequence 直接当作树 parent。
- 产品 Phase 3 的长期记忆、完整执行恢复或具名多分支只保留边界，不在本 change 实现。

## Delivery Phases

产品标签 `[MVP] / [Phase 2] / [Phase 3]` 表示产品交付阶段；下列 **Implementation Phase** 是实施顺序，两套编号不能混用。

| Implementation phase | Product phase | Scope and status |
|---|---|---|
| Phase 1 | MVP | Entry Tree 与安全白名单基础代码/测试已实现；未接入聊天，无 compaction/artifact/Draft |
| Phase 2 | MVP | AgentRun + ChatTurn 原子集成、安全完整消息持久化、取消/恢复、现有 HTTP/SSE；全部待实现，模型仍读旧线性历史 |
| Phase 3 | MVP | Context Projection、适配器边界、旧历史兼容；全部待实现 |
| Phase 4 | Phase 2 | compaction；核心 Run/Projection 之后实施 |
| Optional follow-up | Phase 2 | artifact/TTL；核心 Run/Projection 之后独立批准，不属于 Run 交付 |
| Phase 5 | MVP / Phase 2 | 按对应能力验收兼容、显式历史迁移与旧路径清理；不把 Phase 2 核心双写推迟到此阶段 |

具体约束见 [design](design.md)、[tasks](tasks.md) 与 [Entry Tree](specs/entry-tree/spec.md)、[AgentRun](specs/agent-run/spec.md)、[Context Projection](specs/context-projection/spec.md)、[Context Compaction](specs/context-compaction/spec.md)。本文落实已批准的文档方案，不构成新增代码或迁移实施授权。
