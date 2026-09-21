# Tasks

- [ ] 梳理现有 Eino executor、聊天 service 和题库 service 的接口边界。
- [ ] 明确本机数据目录单实例锁、启动前遗留 `streaming` 原子收敛、`generation_interrupted` 错误码和 client id 幂等语义。
- [ ] 定义 Agent runtime、上下文、工具注册和 Tool Calling 循环接口。
- [ ] 定义 `MemoryCandidate`、`MemoryCandidateStatus` 和 `PendingMemoryCandidate`。
- [ ] 实现题目搜索、题目上下文、作答历史和复盘历史只读工具。
- [ ] 实现单会话单候选、明确确认/拒绝和含糊回复处理。
- [ ] 实现候选到题目、作答、复盘 service 的确认写入映射。
- [x] 定义 Run / Step 事件契约、稳定 `stepId`、事件顺序和重连摘要边界。
- [x] 改造 Graph 路由：同一模型 Step 的文本后出现 ToolCall 时继续执行工具，不提前结束或报协议错误。
- [x] 接入现有流式聊天 API，实时发送有序文本、工具可展示输入/输出摘要、候选状态和 Run 终态事件。
- [x] 改造前端按 UI Message parts 原始顺序渲染 Step 文本和工具活动；历史与重连只恢复允许持久化的内容。
- [ ] 增加 Agent、Step 流、文本后 ToolCall、循环上限、取消、确认、工具错误、重连摘要和失败场景测试。
- [ ] 增加生成中重启、单实例占用、旧 client id 复用、新 client id 重提、无 checkpoint 及 Hub 缺失不误判测试。
- [ ] 增加完整/脱敏 Trace 独立配置、授权门控、告知版本、接收方变化重新授权、撤销停止上报、凭据不上传和错误脱敏测试。
- [ ] 执行 Go 全量测试并完成验收。

## Acceptance checklist

- [ ] 检索工具可以返回题目、作答和复盘上下文。
- [ ] Tool Calling 循环有上限，工具错误和取消可安全结束。
- [x] 同一 Step 中先文本、后 ToolCall 时，首段文本无需等待即可到达前端，ToolCall 仍会执行并继续下一 Step。
- [x] 前端按照到达顺序展示文本与工具活动，且整个 Run 完成前不把任一文本 chunk 误判为最终完成。
- [x] 重连不泄露或伪造完整工具参数和结果；仅恢复持久化文本及脱敏 Step 摘要。
- [ ] 用户确认后才写入长期记忆，拒绝或含糊回复不写入。
- [ ] 原始工具参数/结果、系统提示词、凭据和内部推理不会出现在用户输出、日志或持久化数据中。
- [ ] 现有聊天、会话、题库测试和前端构建继续通过。
- [ ] 完成新增规范验收：普通日志、诊断、UI 摘要与授权 Trace 分离；可选 Trace 平台不成为本地 MVP 依赖。
