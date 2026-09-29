# Proposal

## Why

Step 1–9 已提供本地评测、数据集同步和版本化评估器基础，但目前没有基于已完成 run 创建 CozeLoop 实验并把结果可靠映射回本地执行身份的能力。实验链路需复用既有 report 与已同步数据，不重跑 Agent，并在平台契约未验证时 fail closed。

## What Changes

- [Phase 2] 增加独立实验提交流程，输入已存在的本地 report、与该 Run 精确对应的已同步 run-scoped dataset immutable version 和已发布 evaluator immutable version；若 Step 7–9 sync 不能提供精确版本，则先阻断并 review sync 职责。
- [Phase 2] 提交前核验 live run 的完整身份、无基础设施失败、精确数据集 pinned-version item 集、内容可验证性和字段映射；不使用 latest 或推断的版本。
- [Phase 2] 查询实验状态与分页结果，并从 run-scoped pinned version 验证唯一 `item_id -> run_id + case_id + case_version + repeat_index` 映射；不依赖创建响应顺序或 case ID 单独匹配。
- [Phase 2] 显式处理提交结果未知、部分完成、缺失/重复结果、取消和超时；保存最少化的受限恢复状态，不改变本地质量结论。
- [Phase 2] 对实际包含评测内容的每次远端请求检查内容上传开关和有效授权；撤销后停止后续上传及 pending 重试。
- [Phase 2] 远端 evaluator reason 只允许固定、脱敏原因码，不得回显输入；本地仅持久化白名单分数/状态与原因码，不保存原始 reason。
- [Phase 2] 将公开 IDL/fake HTTP 证据与目标 Workspace 实测分开记录。任何平台闭环结论必须有目标 Workspace 证据；技术 PoC 可先于 10–15 case 人工校准，但未校准的 LLM 分数仅为 advisory。

## Non-goals

- 不重新运行 Agent，不从不包含真实回答的 report 恢复 `actual_output`。
- 不实现或部署自定义评测对象 Endpoint；目标 Workspace 若不支持无评测对象实验，须停止并另行 review。
- 不承诺平台原生 exactly-once；本地请求指纹不等同平台幂等键。
- 不提交含任何基础设施失败样本的 Run；不在本地 report、sidecar、日志或 CI 产物中持久化原始 evaluator reason。
- 不让平台结果覆盖本地 Scorer、关键失败、Judge 或 `ReleaseEligible`。
- 不实现 Prompt A/B 或可信 CI；分别由后续 change 负责。

## Capabilities

### New Capabilities
- `cozeloop-experiment-link`: [Phase 2] 基于既有评测运行提交 CozeLoop 实验、查询结果并安全地回关联执行身份。

### Modified Capabilities
- None.

## Impact

- 领域与报告：`backend/internal/eval` 的运行身份、同步版本和实验结果契约。
- 平台适配：`backend/internal/infrastructure/cozeloop` 的实验 REST adapter、安全传输及授权检查。
- 命令：新增独立实验命令；不把实验状态机继续放入 `backend/cmd/eval`。
- 持久化与文档：受限 run 侧车/恢复状态、`evals/cozeloop/acceptance.md` 及 CozeLoop 操作说明。
- 外部前置：目标 Workspace 的接口、权限、item ID、结果映射、版本快照及重试语义必须实测；未实测时不验收。
