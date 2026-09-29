# Proposal

## Why

[Phase 3，接入完成后的结构优化] CozeLoop 的 Client、Trace、Prompt、评测集、评估器、实验、A/B 和 CI 分阶段接入后，跨模块的配置解释、执行身份、授权状态、字段投影及远端操作顺序容易分散在多个入口。需要在 **Step 1–12 均完成并通过目标 Workspace 验收** 后，以稳定行为和回归证据为基线，系统性降低整个 CozeLoop 实现的修改与验证成本，而不是只修 Step 10–12 的落地问题。

## What Changes

- [Phase 3，验收后] 明确配置/装配、追踪生命周期、Prompt 解析审计、评测执行与同步、评估器发布、实验查询与 A/B 比较、CLI/CI 的模块责任和接口，保留各能力的独立开关和领域 port。
- [Phase 3，验收后] 在保持已验收行为的前提下，收拢重复的版本选择/哈希语义、评测字段投影、运行身份/状态转换及平台传输安全规则；薄化各 CLI，将可测试的远端工作流集中到窄用例。
- [Phase 3，验收后] 固化私有运行产物、授权待上传载荷、可分享摘要的分级契约；为全局追踪、授权撤销、崩溃恢复、固定版本与旧格式兼容增加跨模块回归测试。
- [Phase 3，验收后] 采用小切片、每片测试和可回滚迁移；先确认新实现的真实模块形状和已验收证据，再决定文件重排或合并，避免按当前尚未实现的 Step 10–12 代码作虚构重构。

## Non-goals

- 本 change 不实现 Step 10–12 功能，不替代其 Workspace 验收，也不解决仍未修复的正确性、安全性阻断问题；阻断项须在对应接入阶段先完成。
- 不改变本地 Scorer/Judge 的结论、内容授权条件、Prompt 在线 fallback / 固定版本 strict 策略、生产标签人工发布和平台评分 advisory 边界。
- 不以“重构”名义改变 CLI 对外命令/退出语义、报告可见内容、旧报告/pending 的兼容规则或 CI 发布策略。若接入后发现必须改变这些行为，先提出独立的行为变更 spec 并 review，再决定是否纳入后续阶段。
- 不扩展为无关 Agent 业务/前端重构，不合并成包揽所有平台能力的万能 Client，不为不存在的替代实现新增透传 interface。

## Capabilities

### New Capabilities

- None. 本 change 是已有 CozeLoop 能力验收后的行为保持式重构，`.openspec.yaml` 设置 `skip_specs: true`；验收行为由接入阶段 OpenSpec 与目标 Workspace 证据定义，不凭空增加新需求。

### Modified Capabilities

- None. 不修改既有对外需求；如果基线或产品契约变化，先更新对应接入 change/spec，再调整本 change 的范围。

## Impact

- 后端：`backend/internal/infrastructure/config`、`backend/internal/infrastructure/cozeloop`、`backend/internal/agent/prompt`、`backend/internal/eval`，以及 `backend/cmd/server`、`backend/cmd/eval`、`backend/cmd/cozeloop-evaluators`、授权 CLI 和接入完成后新增的实验/A/B 入口。
- 评测资产与运维：`evals/cozeloop`、本地报告/受限 pending、`.github/workflows/evaluation.yml` 与相关操作文档；只调整内部职责和可测试性，不改变已验收功能或授权语义。
- 前置文档：[整体优化方案](../../../docs/cozeloop-code-optimization-plan.md)、[Step 10–12 接入设计](../../../docs/cozeloop-step10-12-technical-design.md)；必须保留当前未提交的 Step 8–9 成果，不能因重构丢失其行为和验收边界。
