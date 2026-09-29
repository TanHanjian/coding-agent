# Design

## Context

Step 11 依赖 Step 10 的固定数据集/evaluator 版本和可核验的逐 item 实验结果。A/B 两侧应当是不同 run、不同 actual output 数据集快照，但具有相同 case 定义、输入/repeat 和评估条件。现有 `CaseResult.DurationMS` 包含 Judge 阶段，Candidate/Judge Usage 也可能缺失；这些值不得被解释为模型单独延迟或 0 Token。当前没有已批准的 A/B 阈值、样本/repeat 数和人工 reviewer。

## Goals / Non-Goals

**Goals:**
- 读取两份已完成 run/report 与 Step 10 固定实验引用。
- 以显式控制变量指纹判定可比性，并按 case/version/repeat 逐项比较。
- 区分本地质量权威、平台 advisory 分数、成本/Usage 和时延口径。
- 生成差异摘要及人工决策留痕，不自动发布。

**Non-Goals:**
- 不运行或重跑 Agent，不更改 prompt/provider fallback/strict 语义。
- 不将不同 run 的 actual output 写入同一个数据集版本。
- 不依赖 CozeLoop 页面/数组顺序、平台 aggregate 过滤或未经验证的原生 A/B API。
- 不选择胜者、不设置阈值、不切换 production 标签。

## Decisions

### 1. 输入采用两个独立的已验收 run

每侧必须引用自己的 run ID、report、与该 run 绑定的不可变执行清单、真实解析的 Prompt 选择与摘要、同步的数据集固定版本、Step 10 实验 ID/状态及逐 item 引用、精确 evaluator 版本、Git commit、Candidate/Judge 模型及生效参数、provider/endpoint 非敏感身份指纹、工具版本/内容摘要、Context 策略、fixture 内容摘要、各阶段 timeout 和完整 case/version/repeat 身份集合。清单由实际运行时生成并与 `run_id` 及 report digest 绑定，A/B 调用方不得手工填写/覆盖控制变量来“证明”可比。不得从路径名或平台页面顺序推断引用；不得包含凭据、原始 fixture、回答或 Prompt 内容。

### 2. 可比性是显式判断，不做自动“尽力比较”

从各自不可变执行清单与 Step 10 实验引用生成稳定控制变量摘要并逐字段比较。主 Agent Prompt 实际版本可不同；如果 Judge/摘要 Prompt 参与执行，必须固定。Prompt fallback、版本/Workspace/hash 不匹配、清单缺失或非运行时来源、report row `run_id` 不匹配、身份字段缺失/重复、完整预期身份集合不一致、任一基础设施失败、数据集/evaluator 引用不一致或任何平台实验未完成，均标记不可比。可以输出逐项局部事实，但不得以删减后的交集伪装完整对比或出现胜者/发布资格结论。

### 3. 配对身份与分母显式化

通过 `(case_id, case_version, repeat_index)` 配对左右结果；每一 row 的 `run_id` 必须与所属 manifest/report 完全一致，`case_version` 和 `repeat_index` 必须显式存在且不可推断补齐。左右两侧分别与各自完整预期身份清单核对，再比较两集合；共同缺失的样本也必须被检测。任一重复、缺失或不一致身份、基础设施失败、未评分或平台不完整项使整体标记为不可比；显示安全类别及计数，不得用剩余交集生成无条件结论。若 report/experiment 不含可靠身份，则拒绝自动对齐，不以顺序补齐。

### 4. 指标模型保持来源和口径

本地 hard failure、deterministic scorer、Judge、平台各 evaluator 逐项分开展示。CozeLoop 原始自由文本 evaluator reason 不得持久化或进入比较产物；只展示通过 allowlist 的脱敏原因码。Usage 依 Candidate/Judge 区分，缺失即 unavailable。只具备 `DurationMS` 时显示为 case 总耗时（含 Judge），不得计算/命名为 Candidate latency。Token 价格无固定价格表版本时只展示数量，不伪造货币成本。

### 5. 输出与发布决定分离

比较输出由本地生成，可提供 JSON 与 Markdown 只读摘要；敏感字段必须经显式白名单投影，不保留 CozeLoop 原始 evaluator reason。人工决定另记录 reviewer、时间、左右 run/experiment/Prompt 版本、脱敏理由和受控证据引用，禁止复制原始回答、Prompt、工具参数、Judge 理由或 evaluator reason。当前不自动更改 Prompt 标签，不将人工决定编码成隐式命令副作用。

### 6. 平台 A/B 语义

本期的 A/B 能力是对已完成 CozeLoop 实验逐项结果做本地可审计比较；不新增未经 Workspace 验证的 CozeLoop 原生“compare experiments”写接口。如果目标 Workspace 已提供专用比较能力，先记录只读结果与契约并 review 是否需要单独纳入，不以其聚合结果取代本地逐项核验。

### 7. 验证策略

- 单测覆盖相同/不同 controls、Prompt fallback、跨 run、repeat、缺失/重复 case、Usage unavailable 和时间口径。
- fake result 测试覆盖实验结果版本/身份、分页完整性和敏感摘要投影。
- Workspace smoke 验证固定 evaluator/dataset 结果能回关联到相同本地执行身份，并对同条件不同 Prompt 的两次 run 生成完整比较。
- 本地输出只能证明实现正确，不代替 reviewer 对 A/B 结论的签署。

## Risks / Trade-offs

- 控制变量快照不完整会产生虚假因果判断：缺项一律不可比。
- 样本少或 repeat 少可能放大偶然差异：显示分母，不自动定胜负。
- 平台 LLM 模型漂移：保存 evaluator 固定版本/配置引用，并继续标 advisory。
- 报告含真实回答/工具材料或平台 reason 回显：通过白名单摘要并丢弃 CozeLoop 自由文本 reason，避免直接复制原 report。

## Open Questions

- A/B case 子集、repeat 数和筛选策略由谁确认？在确认前仅接受输入中显式列出的相同身份集。
- 质量、Token、时延的判断阈值和 reviewer 未确定；首版不计算“胜者”。
- reviewer、决策记录保存位置与访问权限待确定。
- 是否需要 CozeLoop 原生比较资源仍待 Workspace 契约验证；当前设计不假设该接口存在。
