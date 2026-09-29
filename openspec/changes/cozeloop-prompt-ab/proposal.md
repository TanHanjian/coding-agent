# Proposal

## Why

Step 10 能产出固定版本实验后，还需要用同一组 case 定义对比不同 Prompt 版本的真实运行。若只比较平台平均分或按返回顺序合并，容易掩盖缺失 case、重复执行、基础设施失败、模型/工具配置漂移和成本/时延口径差异，导致错误发布判断。

## What Changes

- [Phase 3] 增加 A/B 比较契约，输入两份独立且已完成的 run/report、与各 `run_id` 绑定的不可变运行清单，以及各自经 Step 10 核验的实验引用。
- [Phase 3] 验证两侧 case/version/repeat 完整身份集合、模型 endpoint/参数、工具版本、上下文/fixture 内容身份、timeout、Judge 和 evaluator 固定版本一致；清单必须由实际运行时生成并绑定 report，唯一允许的控制变量变化为主 Agent Prompt 的实际解析版本。
- [Phase 3] 按 `case_id + case_version + repeat_index` 逐项配对，严格拒绝缺失/不一致/重复身份；分别展示本地质量、平台 advisory 分数、Token/Usage、时延和无效/缺失项，不依赖数组顺序或平台汇总过滤。
- [Phase 3] 输出差异与人工决策记录；在阈值、样本和 reviewer 未获批准前不自动认定胜者、不自动切换 production 标签。
- [Phase 3] 保留受限原件与可分享摘要边界，不在比较报告、人工决策记录或公开摘要中写入回答、Prompt、工具参数、Judge 理由、CozeLoop 自由文本 evaluator reason 或原始错误。

## Non-goals

- 不重跑 Agent，不把两侧 actual output 放进同一数据集版本；每个 run 保留独立输出快照。
- 不将平台 LLM 分数作为本地硬失败或 CI/发布门禁；Step 10 阻断的基础设施失败 Run 不得进入可比 A/B 结论。
- 不自动切换/回滚 Prompt production 标签，不定义未经 review 的胜负阈值。
- 不依赖 CozeLoop 页面顺序、平台总体均值或未验证的原生 A/B endpoint。
- 不修改 Prompt 解析的 fallback/strict 语义；不包含 Step 12 CI 改造。

## Capabilities

### New Capabilities
- `cozeloop-prompt-ab`: [Phase 3] 对两次固定 Prompt run 和对应实验结果执行可比性核验、逐项比较与人工决策留痕。

### Modified Capabilities
- None.

## Impact

- 输入/输出模型：`backend/internal/eval` 中的 run 引用、可比性结果、逐 case 差异和报告投影。
- 命令入口：独立 A/B compare CLI 或现有窄用例入口；具体边界在 design/task review 中确认，不向 `cmd/eval` 增加第二个评测状态机。
- 文档/验收：Prompt Playground 发布流程、CozeLoop acceptance 及 A/B 审阅记录。
- 前置：Step 10 实验提交和逐 item 回关联须通过 Workspace 验收；A/B 控制变量/样本阈值仍待人工 review。
