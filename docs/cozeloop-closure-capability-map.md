# CozeLoop 闭环能力地图

> 状态：方案草案。能力边界与先后顺序已确认，Step 10–12 OpenSpec 已完成本轮一致性 review，待用户最终 review。本文件和 OpenSpec 草案不构成代码修改授权。

## 目标

先补齐并验收 CozeLoop Step 10–12，再执行 `openspec/changes/refactor-cozeloop-integration/` 定义的全链路行为保持式优化。不把公开 IDL、fake HTTP 测试或仓库代码存在误当作目标 Workspace 验收。

## 能力与依赖

| 能力 ID | 范围 | 前置条件 | 完成证据 |
|---|---|---|---|
| `workspace-contract-gate` | 核实 Step 1–9 的目标 Workspace 行为；Step 10 PoC 验证实验 API、无评测对象模式、字段映射、固定版本、精确 item 快照/身份及未知提交恢复语义 | 有授权的测试 Workspace 和非敏感测试数据；10–15 case 人工校准可并行安排，不阻塞传输/身份关联 PoC | `evals/cozeloop/acceptance.md` 中逐项状态、固定版本、逐 item 结果摘要和受控证据引用；校准状态单独记录 |
| `cozeloop-experiment-link` | Step 10：使用已有 report 和与该 Run 精确对应的 run-scoped pinned dataset version 提交/续查实验，并按 item 身份回关联结果 | Step 1–9 Workspace 基线与 Step 10 契约 PoC 通过；验证 Step 7–9 sync 能否提供精确 run-scoped 版本；每个 Run 无基础设施失败；LLM 分数校准未完成时仅作 advisory | 单测、fake HTTP、目标 Workspace 实验结果、精确 item 集及逐 item 对齐证据；若 sync 版本含其他 Run 的 item，则阻断提交 |
| `cozeloop-prompt-ab` | Step 11：比较两个固定 Prompt 版本的独立 run 和实验结果，产出逐 case 差异与人工决策记录 | Step 10 已验收；运行清单、固定变量和样本规则已确认；未校准分数只能 advisory | 可比性检查、缺失/失败样本记录、目标 Workspace 对照证据；不自动决定胜者 |
| `cozeloop-trusted-eval-ci` | Step 12：普通 PR 离线校验；受信任环境的可选固定版本执行、平台状态和脱敏产物 | Step 10/11 契约稳定；离线部分可先实现；启用 live 请求或上传 artifact 前须分别批准模型请求授权、CozeLoop 内容授权、可信触发及 artifact 访问/保留策略 | 离线 PR 与受保护任务证据；未获对应批准时 live/platform/artifact 明确 skipped，不发送内容/不上传产物 |
| `cozeloop-integration-acceptance` | 汇总 Step 1–12 的真实平台、人工校准、A/B、CI 和隐私验收 | 前述能力及外部审核均完成 | 全链路非敏感证据链；无未解释的 pending 项 |
| `refactor-cozeloop-integration` | 当前 change 的 R0–R6 行为保持式代码优化 | 全链路验收通过并冻结行为基线 | 每个切片前后对照、Go 回归、fake HTTP、Workspace smoke 与隐私 review |

## 构建顺序与停止规则

```text
Step 1–9 Workspace 验收
          ↓
Step 10 Workspace 契约 PoC（含 run-scoped snapshot） ── 10–15 case 人工校准可并行
          ↓                         ↓
experiment-link → prompt-ab → trusted-eval-ci（离线先行）
          ↓                         ↓
   Step 1–12 闭环验收（校准完成后才可将 LLM 分数用于门禁）
          ↓
   refactor R0 → R1–R6
```

1. Step 10 PoC 若不支持无评测对象实验、不能以不同 sentinel 验证 Code/LLM 字段映射、不能从 pinned version 核验精确 item 集与唯一身份、或未知 POST 结果不可安全恢复，则暂停实现/上线；不擅自切换至部署 Agent Endpoint。PoC 必须覆盖两个 Run、重复执行和部分写入重试。
2. Step 10 报告含任一基础设施失败样本时，整次实验提交必须阻断；不得把它评分成普通质量失败或静默排除。CozeLoop 原始 evaluator reason 不进入本地 report、sidecar、日志或 CI 产物；仅保存白名单分数/状态和脱敏原因码，evaluator 不得回显输入内容。
3. 10–15 case 人工校准不阻塞传输/身份关联 PoC，但校准完成前 CozeLoop LLM 分数仅为 advisory，不得用于质量门禁或发布结论。模型 endpoint 请求、CozeLoop 内容上传和 artifact 上传分别授权；任何一项未批准时对应 CI 阶段保持 disabled/skipped。
4. 目标 Workspace 不可用时，可 review spec、开发 fake adapter 并做本地测试，但不得将对应平台能力标为验收完成。每个代码切片都需要独立确认；当前工作树中已有的 Step 8–9 和文档修改必须保留。

## 验证层次

- **本地静态/单测：**验证领域规则、身份、状态和隐私投影。
- **Fake HTTP：**验证公开契约映射、分页、取消、错误脱敏和逐请求授权；不证明目标 Workspace 支持。
- **目标 Workspace：**验证实际权限、字段、固定版本、item 回关联、真实实验结果与重复提交语义。
- **人工/安全验收：**完成 evaluator 校准、A/B 复核及 CI 授权、artifact 访问与保留策略。
- **代码优化回归：**全链路完成后，以固定行为基线证明重构未改变外部行为。
