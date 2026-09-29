# CozeLoop 评估器验收记录

workspace_verification=pending

## Code 评估器在线一致性

- 状态：pending
- 本地验证：Code 资产遵循公开文档中的 `exec_evaluation(turn)` / `EvalOutput(score, reason)` 约定；本地 fixture 与确定性 scorer 的规则映射测试通过。
- Workspace 验证：尚未在目标 Workspace 创建、Validate、Debug 或发布这些评估器；公开文档和本地测试不代表 Workspace 验收通过。
- 后续证据：待记录目标 Workspace、评估器精确版本、测试样例及 Validate/Debug 结果；不得记录 API Token 或其他凭据。

## LLM 评估器人工校准

- 状态：pending
- 样本要求：选取 10–15 个 case；记录以下四个维度各自的人工分数和 CozeLoop 分数：`answer-faithfulness`、`instruction-following`、`answer-completeness`、`answer-actionability`。人工与平台分数均按 0–4 记录。
- 评估器版本：必须记录每个维度实际使用的不可变版本；不得以标签或“latest”代替。
- 差异定义：`差异 = CozeLoop 分数 - 人工分数`。记录差异原因和复核结论；校准阈值及是否可用于门禁需另行 review，本模板不预设通过标准。
- 隐私：只记录 case 标识、版本、分数、差异、脱敏说明和受控证据引用；不要粘贴原始回答、Prompt、API Token 或其他凭据。

### 校准批次信息

- 目标 Workspace：待填写
- 校准日期：待填写
- 校准人 / 复核人：待填写
- 本地 run / 报告引用：待填写
- 选择的 evaluator 精确版本：待填写
- 证据位置（不含凭据）：待填写
- 复核结论：pending

### Case 样本清单（填写 10–15 个不同 case）

| # | case_id | case_version | 本地 run / 报告引用 | 备注 |
|---:|---|---|---|---|
| 1 | 待填写 | 待填写 | 待填写 | |
| 2 | 待填写 | 待填写 | 待填写 | |
| 3 | 待填写 | 待填写 | 待填写 | |
| 4 | 待填写 | 待填写 | 待填写 | |
| 5 | 待填写 | 待填写 | 待填写 | |
| 6 | 待填写 | 待填写 | 待填写 | |
| 7 | 待填写 | 待填写 | 待填写 | |
| 8 | 待填写 | 待填写 | 待填写 | |
| 9 | 待填写 | 待填写 | 待填写 | |
| 10 | 待填写 | 待填写 | 待填写 | |
| 11 | 待填写 | 待填写 | 待填写 | |
| 12 | 待填写 | 待填写 | 待填写 | |
| 13 | 待填写 | 待填写 | 待填写 | |
| 14 | 待填写 | 待填写 | 待填写 | |
| 15 | 待填写 | 待填写 | 待填写 | |

### 评分记录（每个 case 对每个维度填写一行）

| case_id | 维度 | evaluator_key@version | 人工分数 (0–4) | CozeLoop 分数 (0–4) | 差异 (平台−人工) | 差异说明 / 复核结论 | 脱敏证据引用 |
|---|---|---|---:|---:|---:|---|---|
| 待填写 | faithfulness | answer-faithfulness@待填写 | | | | | |
| 待填写 | instruction following | instruction-following@待填写 | | | | | |
| 待填写 | completeness | answer-completeness@待填写 | | | | | |
| 待填写 | actionability | answer-actionability@待填写 | | | | | |

完成 10–15 个样本并经人工复核前，LLM 分数仅作 advisory，不得成为 CI 或发布硬门禁。本模板已就绪不代表 Workspace 验收或校准已经完成。
