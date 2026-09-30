# Proposal

## Why

当前聊天持久化以线性 `conversation.Message`、`sequence` 和预创建的 `streaming assistant` 为中心，无法完整表达工具调用、工具结果、会话分支以及可复用的上下文压缩。`ContextManager` 仍接收旧的可见消息列表，Eino 转换器也无法恢复工具协议。

本 change 将第一层重构为参考 Pi 语义的 append-only `ConversationEntry` 树，并通过上下文投影转换为 provider-neutral `AgentMessage`，最后转换为 Eino `schema.Message`。`AgentRun` 保留为一次外部请求的执行生命周期，但不保存消息正文、不替代消息树，也不决定模型上下文。

## What Changes

- [MVP] 增加带 `parentId` 和 `activeLeafId` 的会话 Entry Tree，支持分支、幂等追加和安全工具投影。
- [MVP] 将一次用户请求收敛为 `AgentRun`，承载状态、取消、失败、重启收敛和 client id 幂等语义；运行事件不进入模型上下文。
- [MVP] 增加从 active leaf 到 provider-neutral `AgentMessage` 的 Context Projection，并替换旧的线性 history 到 Eino 的转换边界。
- [Phase 2] 增加持久化 `compaction` entry、合法压缩边界、摘要校验、CAS 和重启复用。
- [Phase 2] 允许工具按工具级白名单持久化有界安全投影；完整大结果只通过受限、带 TTL 的 artifact 引用访问。
- [Phase 2] 保留旧 `messages` 作为兼容 read model，完成双写、回填和逐步切换后再评估移除旧线性路径。

## Non-goals

- 不在本 change 中实现完整 checkpoint、模型执行恢复或非幂等工具重放。
- 不永久保存完整原始工具参数、完整工具结果、凭据、内部推理或 provider 原始响应。
- 不引入具名 Branch 表；第一版只维护 Pi 式单 `activeLeafId`，同一会话同时只允许一个活跃 Run。
- 不改造前端视觉界面，不引入第三方记忆服务，不迁移 Eino ADK 作为本 change 的隐含前置条件。
- 不删除现有用户可见 `messages`，除非后续独立迁移验收明确批准。

## Delivery Phases

- **MVP**：Entry Tree、AgentRun 生命周期、基础 Context Projection、兼容旧消息 read model。
- **Phase 2**：持久化压缩节点、摘要协调、安全工具投影和临时 artifact。
- **Phase 3**：长期记忆、完整执行恢复或具名多分支；本 change 只预留边界，不实现。
