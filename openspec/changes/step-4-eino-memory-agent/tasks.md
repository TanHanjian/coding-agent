# Tasks

- [ ] 梳理现有 Eino executor、聊天 service 和题库 service 的接口边界。
- [ ] 定义 Agent runtime、上下文、工具注册和 Tool Calling 循环接口。
- [ ] 定义 `MemoryCandidate`、`MemoryCandidateStatus` 和 `PendingMemoryCandidate`。
- [ ] 实现题目搜索、题目上下文、作答历史和复盘历史只读工具。
- [ ] 实现单会话单候选、明确确认/拒绝和含糊回复处理。
- [ ] 实现候选到题目、作答、复盘 service 的确认写入映射。
- [ ] 接入现有流式聊天 API，增加候选状态结构化事件并隐藏工具过程。
- [ ] 增加 Agent、工具、循环上限、取消、确认和失败场景测试。
- [ ] 执行 Go 全量测试并完成验收。

## Acceptance checklist

- [ ] 检索工具可以返回题目、作答和复盘上下文。
- [ ] Tool Calling 循环有上限，工具错误和取消可安全结束。
- [ ] 用户确认后才写入长期记忆，拒绝或含糊回复不写入。
- [ ] 工具参数、系统提示词、凭据和内部推理不会出现在用户输出或日志。
- [ ] 现有聊天、会话、题库测试和前端构建继续通过。
