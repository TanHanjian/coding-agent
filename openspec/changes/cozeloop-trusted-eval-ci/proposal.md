# Proposal

## Why

现有 `evaluation.yml` 保持 PR 离线校验，但 live smoke 是 advisory，且当前 artifact 路径可能直接上传完整 report 目录。Step 12 需要在不向不可信 PR 暴露 Secrets 的前提下，区分本地执行/质量/平台状态，限制报告可见字段，并只在受保护环境和经批准的内容授权下尝试 CozeLoop 实验。

## What Changes

- [Phase 3] 保持普通 PR 无 CozeLoop/模型 Secret 依赖，只执行离线测试、manifest 校验和安全的报告验证。
- [Phase 3] 将可选 live/平台任务限制在可信触发条件和受保护环境；不使用 `pull_request_target` 执行不可信代码并读取 Secrets。模型 endpoint 请求与 CozeLoop 内容上传分别授权；任一授权未批准时对应请求保持 disabled/skipped。
- [Phase 3] 将 `execution_status`、`quality_status`、`model_request_status`、`platform_status`、`artifact_status`、`eligibility_status` 分开表达；缺少凭据/授权时显式 skipped，平台错误不覆盖本地质量状态，skipped 不得伪装成 quality pass。
- [Phase 3] 对每个携带评测内容的请求重查授权；不得自动生成或伪造 consent。授权机制未获批准时，平台提交必须禁用或跳过。
- [Phase 3] 关闭现有全量 `evals/reports/` artifact 上传；artifact 访问/保留策略获批准前不上传任何 CI artifact。获批后也仅能上传白名单脱敏摘要。
- [Phase 3] 可信 live 执行必须固定 Prompt/evaluator immutable version 并显式启用严格 Prompt 解析；平台评分保持 advisory，普通 PR 离线成功、可信任务失败及跳过状态均有清晰 CI 语义。

## Non-goals

- 不在本 change 批准或实现新的非交互式内容授权政策；授权责任、方式、访问策略须先由相关负责人审批。
- 不把 CozeLoop 平台分数设为 CI/发布硬门禁，不改变本地 Scorer/Judge 权威性。
- 不让普通 PR/fork 获得 CozeLoop 或模型 Secrets；不使用不可信 PR 的 privileged workflow。
- 不自动重新运行 Agent 或在 CI 中改变 Prompt production 标签。
- 不在未定义 artifact 访问者/保留期的情况下发布原始报告或私有链接。

## Capabilities

### New Capabilities
- `cozeloop-trusted-eval-ci`: [Phase 3] 安全区分离线 PR 与可信 live/platform CI，实施逐请求授权、状态分类和白名单脱敏产物。

### Modified Capabilities
- None.

## Impact

- CI：`.github/workflows/evaluation.yml` 的 PR/可信触发分流、授权前置检查、报告 artifact 生成。
- 后端输出：`backend/internal/eval` 或 `backend/cmd` 的机器可读执行/质量/平台/资格状态与安全摘要。
- 文档/安全策略：`evals/README.md`、CozeLoop acceptance、Secret 和 artifact 访问/保留说明。
- 外部前置：受保护环境/触发规则、非交互授权主体、撤销方式、artifact 访问权限与保留期需明确批准；未批准部分保持关闭/跳过。
