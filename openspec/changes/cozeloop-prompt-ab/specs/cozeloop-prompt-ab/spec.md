# Spec Delta

## Purpose

定义两个独立、固定 Prompt 版本 run 的可比条件、逐 case/repeat 结果关联、指标解释和人工决策留痕，避免不完整或有变量漂移的数据生成自动发布结论。

## ADDED Requirements

### Requirement: A/B 运行可比性
[Phase 3] A/B 输入 MUST 包含两个不同 `run_id` 的已完成本地报告、各自经 Step 10 核验的固定版本实验引用，以及由实际运行时生成并绑定对应 `run_id` 和 report digest 的不可变执行清单。清单 MUST 包含实际 Prompt 解析版本、provider/endpoint 非敏感身份指纹、模型及生效参数、工具版本/内容摘要、Context 策略、fixture 内容摘要、各阶段 timeout、完整 case/version/repeat 身份集合、Judge/evaluator 不可变版本及数据集/experiment 逐 item 引用；MUST NOT 由 compare 调用方手工填写或覆盖来证明可比。两侧除主 Agent Prompt 的实际解析版本外，所有控制变量与完整身份集合 MUST 一致。Prompt 请求值、实际解析版本、来源、fallback 和内容摘要 MUST 可核验，清单不得包含凭据或原始评测内容。

#### Scenario: 两侧仅主 Prompt 版本不同
- **WHEN** 两个 run 的运行时清单来源可信且完整、各自 report digest/run ID 绑定正确，控制变量和完整身份集合相同，实际主 Prompt 版本不同且均严格解析成功
- **THEN** A/B 工具 MUST 将其标记为可比，并保留两侧各自的 run、dataset、experiment、Prompt 和 evaluator 版本引用

#### Scenario: 存在控制变量漂移
- **WHEN** 运行时清单缺失/被调用方声明/篡改、row `run_id` 与 manifest 不符、case identity 缺失/重复、任一侧与完整预期身份清单不符、模型参数/endpoint/工具/上下文/fixture/timeout/Judge/evaluator 版本或 case/repeat 集不同，或任一侧发生 Prompt fallback/版本不匹配
- **THEN** A/B 工具 MUST 标记为不可比并说明安全差异，不得用共同交集掩盖缺失样本或生成胜出/发布资格结论

### Requirement: 逐 case 与 repeat 对齐
[Phase 3] A/B MUST 按 `case_id + case_version + repeat_index` 配对两侧结果；每一 row MUST 显式具有与所属 report/manifest 一致的 `run_id`、`case_version` 和 `repeat_index`，不得推断补齐。两侧分别 MUST 与各自完整预期身份清单核对，以检测共同缺失项；不得依赖数组顺序、case ID 单独匹配或合并不同 repeat。每侧仍须保留自己的 run 身份。缺失、重复、基础设施失败、未评分及实验结果不完整的执行 MUST 导致整体不可比并显示安全类别/计数，不得用剩余交集生成无条件结论或静默过滤。

#### Scenario: 可用身份集合完整
- **WHEN** 两侧结果都能以唯一 case/version/repeat 身份配对
- **THEN** 工具 MUST 对每一对输出本地结论、平台结果和差异

#### Scenario: 一侧缺少或重复执行
- **WHEN** 任一侧 case/repeat 缺失、重复、row identity 不匹配、基础设施失败或平台结果不完整
- **THEN** 工具 MUST 将整体标记为不可比，显示不可配对项及安全原因，且 MUST NOT 以剩余样本生成无条件胜者结论

### Requirement: 指标口径与缺失值
[Phase 3] 比较结果 MUST 分开展示本地 hard failure、确定性分、本地 Judge、CozeLoop evaluator、Candidate/Judge Usage 和时延，并显示样本数/有效分母。缺失 Usage MUST 表示 unavailable 而非 0；包含 Judge 的总 case 时延 MUST NOT 标作 Candidate 时延。未验证价格时 MUST NOT 推算货币成本。

#### Scenario: Usage 不可用或时延口径不匹配
- **WHEN** 某一侧未返回 Usage，或现有时延只包含完整 case/Judge 阶段
- **THEN** 比较输出 MUST 显示 unavailable 或正确的总 case 时延口径，不得把缺失值当零或伪称模型时延

#### Scenario: 平台分数与本地结论不同
- **WHEN** CozeLoop evaluator 结果与本地 scorer/Judge 不同
- **THEN** 工具 MUST 并列展示来源、版本及差异，且本地关键失败和发布资格保持权威

### Requirement: 人工决策与发布边界
[Phase 3] A/B 工具 MUST 仅输出数据差异和可审计的人工决策记录，不得自动选择胜者、自动改变 production 标签或将未校准平台分数用作硬门禁。阈值、样本策略和人工审批未获批准时，输出 MUST 明确标注“仅供比较/无发布结论”。

#### Scenario: 尚无已批准阈值或 reviewer 决定
- **WHEN** A/B 数据完整但质量/成本/时延阈值或人工审批缺失
- **THEN** 工具 MUST 输出事实与差异，不得宣称某版本胜出或可发布

#### Scenario: 人工决定已记录
- **WHEN** reviewer 对可比 A/B 结果作出决定
- **THEN** 决策记录 MUST 可关联左右 run/实验/Prompt 版本、经核验的 reviewer 身份、时间、脱敏理由和受控证据引用，且不得写入原始 evaluator reason 或评测内容，不得自动执行标签切换

### Requirement: A/B 平台验收与隐私
[Phase 3] 本地 A/B 对比与目标 Workspace 平台验收 MUST 分开记录。比较报告、人工决策记录、可分享摘要和 CI 产物 MUST 遵守报告可见性契约，不得持久化 CozeLoop 自由文本 evaluator reason，也不得包含原始回答、Prompt、工具参数、Judge 理由或未脱敏错误；仅允许分数/状态和 allowlist 脱敏原因码。

#### Scenario: 本地对比通过但无 Workspace 证据
- **WHEN** 本地 report 与 fake experiment 结果可对比，但目标 Workspace 实验与 item 回关联未验收
- **THEN** 对比 MUST 保持平台验收 pending，不得报告 Step 11 闭环完成

#### Scenario: 摘要生成遇到敏感材料
- **WHEN** 输入 report 包含受限回答、Prompt、工具参数或原始错误
- **THEN** 可分享摘要 MUST 仅投影白名单字段，不得泄漏这些原始材料
