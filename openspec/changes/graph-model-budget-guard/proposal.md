# Proposal

## Why

工具循环会持续增加模型输入，跨轮准备不能代替每次请求检查。当前固定模型窗口、计数漏字段和未设置输出上限，使预算策略无法约束真实请求。

## What Changes

- [Phase 2] **BREAKING**：启用聊天时必须显式配置窗口、输出上限、安全余量与输出参数模式，不再通用默认 128000。
- [Phase 2] 检查完整消息和工具定义，覆盖每次聊天及摘要请求；超限或不能计数时拒绝发出请求。
- [Phase 2] 容量失败复用 Run 原子收尾，实时、重连和历史展示固定安全原因，保留已有可见文本和事实。

### Non-goals

不迁移 ADK，不新增运行内摘要或 artifact，不改变 Entry Tree schema，不替用户猜测实际端点容量。

## Capabilities

### New Capabilities

- `model-call-budget`: [Phase 2] 模型请求的完整输入估算、容量配置、逐次防护与受控失败反馈。当前主规范目录为空，本能力独立记录；既有未归档变更中的 context-compaction 不作为主规范覆盖。

### Modified Capabilities

无。

## Impact

影响 server 装配、模型配置/客户端、Graph Builder、摘要 invoker、Run 错误分类、generation hub、SSE 与历史 UI。保持 Graph 运行时和既有存储事务，不增加依赖。独立 eval judge 保留自己的客户端配置语义。
