# Proposal

## Why

Step 7 已为本地评测结果建立 CozeLoop 评测集同步基础，但平台侧 Code 与 LLM 评估器仍未落地。需要将可精确复现的断言和可人工校准的回答质量维度纳入版本化评测流程，同时保留本地评测能力，并清楚区分代码测试与目标 Workspace 验收。

## What Changes

- [Phase 2] 定义 Code 评估器，覆盖现有 case 可表达的非空输出、必含/禁含内容、执行状态及可选 Tool Trace 结构检查，并用本地 case 验证与对应本地断言的一致性。
- [Phase 2] 定义四个可独立版本化的 LLM 评估维度：忠实度、指令遵循、完整性和可执行性；映射问题、材料、实际回答及参考事实，支持记录 10–15 个 case 的人工校准结果。
- [Phase 2] 仅在官方公开 API/IDL 明确支持时，通过隔离的 adapter 创建或查询评估器；若缺少明确契约，则提供版本化定义和手动配置/验收说明，不猜测或调用未证实的 endpoint。
- [Phase 2] 将平台评估结果保持为本地 scorer/Judge 之外的独立结果；关键失败规则继续由本地 scorer 执行，LLM 维度完成校准前不得成为 CI 硬门禁。
- [Phase 2] 明确记录仓库自动化测试与目标 Workspace 实测的不同验收状态；fake server 和公开接口实现不代表 Workspace 权限、字段行为、版本发布或评估结果已验收。
- [Phase 2] 评估器调试或发布涉及完整评测内容时，沿用现有明确授权与撤销检查，不上传凭据，失败信息保持脱敏。

## Non-goals

- 不替换或削弱本地确定性 scorer、关键失败规则、现有 Judge 或本地报告。
- 不将未校准的 CozeLoop LLM 评分设为 CI/发布硬门禁。
- 不推断目标 Workspace 已开放某个评估器 API；不在缺少官方契约时实现猜测的 endpoint。
- 本 change 不声称已完成目标 Workspace 的在线 API、权限、字段映射、版本管理或评估结果实测。
- 不自动提交评测实验或实现 Prompt A/B；这些属于后续步骤。

## Capabilities

### New Capabilities
- `cozeloop-evaluators`: [Phase 2] CozeLoop Code 与 LLM 评估器的定义、字段映射、版本跟踪、校准记录和验收边界。

### Modified Capabilities

- None.

## Impact

- 现有评测域：`backend/internal/eval` 中的 case、deterministic scorer、Judge、报告和数据集字段映射。
- CozeLoop 基础设施：`backend/internal/infrastructure/cozeloop` 中已有评测集 REST adapter；只有官方公开 API/IDL 足以支撑时才扩展评估器操作。
- 评测资产与文档：`evals/` 中的样例/校准记录，以及 CozeLoop 集成技术设计和使用说明。
- 外部平台：目标 CozeLoop Workspace 的真实能力与权限需作为独立手工验收项，不由 fake server 测试替代。
