# CozeLoop 全链路接入：现状、剩余实施方案与验收计划

- 状态：Draft，待讨论与 review；**不是开工授权**
- 范围：原方案 Step 1–12，从 Trace、Prompt Hub/Playground 到评测集、评估器、实验、A/B 与 CI
- 基线：[平台集成技术方案](cozeloop-platform-integration-technical-design.md)、[Prompt 发布流程](prompt-playground-release.md)、[评测操作说明](../evals/README.md)、[评估器 OpenSpec](../openspec/changes/add-cozeloop-evaluators/design.md)
- 证据原则：仓库代码、本地测试、目标 Workspace 实测分别记状态；公开文档/IDL 和 fake HTTP 测试不能替代目标 Workspace 验收。已确认：实验接入必须经过目标 Workspace 实测才能认定完成；实验提交先采用独立命令，复用已有报告和已同步的数据，不重跑 Agent。本文不声称已完成外部验证。

## 1. 目标与边界

目标是打通“固定 Prompt 版本 → 本地 Go/Eino Agent 和 Judge 执行 → 可关联 Trace/Usage → 版本化评测集 → 固定版本评估器 → 实验结果 → A/B 对比 → 受控 CI”的闭环。用户在本地仍可完全不启用 CozeLoop 运行 Agent 和评测。

- CozeLoop 是可选的平台适配层；本地 Runner、确定性 Scorer、关键失败和 `ReleaseEligible` 仍是发布判断的基础。未经人工校准的平台 LLM 分数只作 advisory，不能改变本地硬失败结论。
- Playground 验证单轮 Prompt；完整 Tool Loop、SQLite fixture、取消/超时与上下文行为仍由本地 Runner 验证。不在 Prompt 中迁移工具 schema、循环上限和其他业务约束。
- 第一阶段实验沿用“本地执行、输出回流、平台无评测对象实验”的既有意向，但**是否受目标 Workspace 支持是待验证前提**。自定义 RPC/A2A/Sandbox 评测对象不在本轮；如果无评测对象实验不受支持，先回到方案 review，不暗中改用部署 Agent Endpoint。
- 不重做已有数据集/评估器适配器，不把平台 SDK/HTTP DTO 传进 `backend/internal/eval`。不承诺自动移动 Prompt production 标签或把平台评分立即设为 CI 门禁。

## 2. 当前事实与缺口（按原方案 Step）

“代码已有”指仓库实现；“平台待验”指缺少目标 Workspace 的真实权限/API/字段/结果证据。当前 [评估器验收记录](../evals/cozeloop/acceptance.md) 仍是 `workspace_verification=pending`。

| Step | 仓库现状及依据 | 剩余工作 / 验收状态 |
| --- | --- | --- |
| 1 Client/配置 | `backend/internal/infrastructure/config/config.go`、`backend/internal/infrastructure/cozeloop/client.go` 已有默认关闭、凭据校验、SDK 生命周期 | 核对目标环境凭据与 Workspace，不将本地单测当作连通性证明 |
| 2 Eino Trace | `backend/internal/infrastructure/cozeloop/tracing.go`、`backend/cmd/server/main.go`；已接 callback、内容授权门控 | 目标 Workspace 实测层级、候选/Judge 可定位性、关闭/撤销后的上报行为 |
| 3–4 PromptProvider / Hub | `backend/internal/agent/prompt/`、`backend/internal/infrastructure/cozeloop/prompt_provider.go`；在线回退与固定版本严格路径已接入 | 确认目标 Workspace Prompt key、变量、版本、标签与严格解析；固定版本不能用本地回退冒充 |
| 5 Playground / 发布 | `evals/interview-agent-playground.v1.json`、`docs/prompt-playground-release.md`；本地 CLI 校验场景 | 平台草稿/版本/标签仍需人工操作与留证；本地清单校验≠调用平台 Playground |
| 6 评测身份/Trace | `backend/internal/eval/run_observability.go`、`backend/cmd/eval/main.go`；run/case/version/repeat，Candidate 与 Judge 的 Trace ID/Usage 分开记录 | 目标 Workspace Trace 及报告字段逐项对照，Usage 不可用时保持空值 |
| 7 评测集同步 | `backend/internal/eval/dataset_sync.go`、`backend/internal/infrastructure/cozeloop/evaluation_client.go`；已支持建集、数据项/版本、内容哈希、pending 续传 | 验证目标 Workspace 的权限、schema、版本快照、同 run 重试、不同 run/repeat 不覆盖及部分写入恢复 |
| 8–9 Code/LLM 评估器 | `evals/cozeloop/`、`backend/internal/eval/evaluator.go`、`backend/internal/infrastructure/cozeloop/evaluator_client.go`、`backend/cmd/cozeloop-evaluators/main.go`；清单、映射、Validate/BatchDebug 和显式发布 CLI 已有 | [OpenSpec tasks](../openspec/changes/add-cozeloop-evaluators/tasks.md) 的 3.2–4.2 尚未勾选：校准模板、操作文档、回归/review；平台 runtime/model/版本与结果未验，10–15 case 未校准 |
| 10 自动实验 | 原方案有构想；现有 `EvaluationPlatform` 仅含评测集接口，`--publish-cozeloop` **只同步数据集** | 先验证实验 API/权限和无评测对象模式，再做独立领域 port、adapter、提交/查询、报告关联和 CLI 编排 |
| 11 Prompt A/B | 现有固定 Prompt 版本评测和报告字段可作为输入 | 尚无两次实验的同条件对齐、结果拉取/对比与发布决策记录 |
| 12 CI | `.github/workflows/evaluation.yml` 已有离线 validate、无凭据时跳过的 advisory live smoke 与报告 artifact | 尚无平台实验提交、固定 Prompt 版本的受控 CI 路径；不得让普通 PR 检查依赖 CozeLoop |

注意：`docs/cozeloop-integration-plan.md` 是早期仅覆盖 Trace 的 Proposal；不能用它取代上述全平台范围。原平台方案中的计划文件树和部分建议 CLI 参数不是当前仓库已实现的接口。

## 3. 能力边界与数据契约

```text
Prompt Hub（固定版本） ─→ 本地 Agent/Eval Runner ─→ 本地 Report（权威）
                                  │                 │
                           CozeLoop Trace      评测集版本（已接 adapter）
                                                     │
                                         固定评估器版本（已接 adapter）
                                                     │
                                        实验提交/结果查询（待验证、待实现）
                                                     │
                                         A/B 与 CI advisory（待实现）
```

1. **身份**：case 定义使用 `case_id + case_version`；一次 live 执行有唯一 `run_id`；结果身份使用 `run_id + case_id + case_version + repeat_index`。相同 commit/model/prompt 的新运行仍是新 run；重试原执行复用原 run 和内容哈希，不能覆盖其他运行。
2. **输入与输出**：已有 `DatasetItem` 存本地 case 的输入/期望、Candidate 的实际输出、脱敏工具轨迹、本地评分/错误分类、Candidate/Judge Trace ID 和 Usage、Prompt 解析元数据。实验必须引用**确定的数据集版本快照**和每个评估器的**具体不可变版本**，不得使用标签、草稿或“最新版本”代替。
3. **结果对齐**：实验完成后需要将平台结果按经目标 API 验证的 item/结果标识映射回本地执行身份；不得只按 case ID 合并，不能依赖平台返回顺序作为唯一关联。若 API 不提供可靠外部键，则建立可审计的本地映射并验证其重复提交语义；在确认前不宣称幂等。
4. **失败语义**：Prompt 严格解析失败、平台提交失败、评估器调用失败分开记录。前者使该次本地评测无发布资格；后两者不覆盖或删除本地报告，平台结果不能伪造为成功。实验超时、部分结果、空 score/reason 均需明确状态，不能按缺失即 0 或跳过后判通过。
5. **内容授权**：`COZELOOP_CAPTURE_CONTENT` 只控制 Trace 内容；评测内容上传需 `COZELOOP_EVALUATION_ENABLED`、`COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED` 与匹配 Workspace/API 地址的有效 `evaluation-dataset-content` 授权。新的实验提交/结果接口先按是否包含 case/回答/Prompt/工具内容分类，凡上传内容均逐请求复核授权并脱敏凭据及原始错误；撤销后不继续发送 pending 内容。已上传数据不会因本地撤销自动删除。

## 4. 剩余实施顺序与 review 门槛

每个阶段先 review 对应 OpenSpec 的需求、设计和任务，再单独确认是否开始改代码；阶段间以验收结果推进，不因本文列出计划而自动开工。

| 阶段 | 交付与建议落点 | 可验证的完成条件 / 门槛 |
| --- | --- | --- |
| A. 评估器收尾（Step 8–9） | 按 `openspec/changes/add-cozeloop-evaluators/` 补校准模板、`evals/README.md` 操作说明与已有方案状态；检查当前 CLI/清单/资产；完成 Go 回归与 spec review | `validate` 离线无凭据；`debug`/`publish --apply` 遵守显式输入、授权、固定版本哈希；记录 10–15 case 的人工/平台分数、版本和差异的模板。未实测前 acceptance 保持 pending |
| B. Workspace 契约验证（Step 1–9 + Step 10 前置） | 在有权限的目标 Workspace 用不含凭据的证据记录 Prompt、Trace、评测集、评估器和实验 API 的可达性、权限、响应、字段、版本与重复提交语义；必要时修订 OpenSpec | 分别记录“已验证/失败/待验证”，真实 Code runtime 与 LLM model config 可用，评估结果包含完整 score/reason；明确无评测对象实验是否支持。任何不兼容先停下评审方案 |
| C. 自动实验（Step 10） | 为实验新建 OpenSpec change；在 `backend/internal/eval` 增加最小实验 port、状态/结果与身份映射；在 `backend/internal/infrastructure/cozeloop` 加隔离 adapter；新增独立实验提交命令，以已有报告和已同步的数据集版本为输入，不重新运行 Agent；扩展报告/操作说明 | 仅使用已同步且内容校验过的数据集固定版本与已发布评估器版本；重复提交同一 run 不产生不可控重复实验，冲突停下；轮询有超时/取消/分页边界；报告保留本地结果、实验 ID/状态、版本引用、逐 case 对齐与安全失败分类；fake HTTP 测试及目标 Workspace 实测均通过后才认定实验接入完成 |
| D. Prompt A/B（Step 11） | 为 A/B 新建 OpenSpec change；定义两个独立 run 的对比清单/汇总，固定 case 版本、模型/参数、工具、上下文策略和评估器版本，仅改变 Prompt Version | 两次运行各有可定位的本地报告、Trace、数据集/实验版本；可逐 case 比较分数、错误、Token 和耗时；基础设施失败或未完成的 case 不被静默剔除；production 标签仍经人工审核切换及可回滚 |
| E. 受控 CI（Step 12） | 为 CI 新建 OpenSpec change；沿用现有 `evaluation.yml` 的离线 PR validate 和 advisory smoke，在受信任上下文中增加**可选**固定版本评测/实验作业 | 普通 PR 无平台凭据也通过离线校验；无密钥/有效授权时明确跳过上传且保留可用本地报告；平台失败不冒充质量通过；报告 artifact/实验链接不含凭据；未经校准和单独评审不升级平台评分为硬门禁 |

**阶段 B 的位置是阻断式的**：可以先做阶段 A 的离线收尾；不能在不确认目标 Workspace 实验能力的情况下，将阶段 C 的公开服务端 IDL 当成已可用产品 API。若暂时拿不到 Workspace 权限，阶段 C 最多形成待验证的 spec 与 fake adapter 验证，不能标记实验接入或平台闭环完成。独立命令的输入、失败续传和关联格式在阶段 C 的 OpenSpec 中明确，不在本文虚构现成 CLI 参数。

## 5. 验证矩阵

| 验证层 | 核查内容 | 通过的含义 |
| --- | --- | --- |
| 静态与本地 | 清单引用/内容哈希、Code/LLM 资产、身份映射、授权与撤销、固定 Prompt 版本/报告资格；`backend` 的 Go tests 与 vet | 仅证明本地规则和代码无回归 |
| Fake HTTP | 公开契约的端点/DTO、分页/轮询、相同 run 重试、冲突、部分结果、超时/取消、错误脱敏、逐请求授权检查 | 仅证明与测试使用的契约映射一致 |
| 目标 Workspace | Prompt/Trace 实际关联；评测集数据项和不可变版本；评估器创建、运行、发布及校准；实验提交、状态、结果映射、A/B 展示与重传 | 有记录在 `evals/cozeloop/acceptance.md` 的真实环境、版本、非敏感请求/结果摘要或链接，才能将相应项标记验收 |
| CI | PR 离线检查、可信任务固定版本、无密钥跳过、有授权才上传、artifact 与失败归因 | 不改变普通 PR 的本地可运行性 |

验收时尤其要核查 24 条现有 case 的预期范围与实际过滤条件、多个 `repeat_index`、新 run 与旧 run 隔离，以及 Candidate/Judge Usage 缺失时不误报为 0。10–15 case 的人工校准覆盖四个 LLM 维度；在样本、差异阈值和审核人未确认前仅记录差异，不自动生成合格结论。

## 6. 风险、回滚与未决决策

| 风险 | 处理与回滚 |
| --- | --- |
| 目标 Workspace 与公开 IDL 不一致，或不支持无评测对象实验 | 停止实验适配和发布，记录实际响应并修订 spec；本地 Runner、报告与数据集同步不受影响 |
| 部分写入、重试产生重复实验或结果错配 | 先确认真实外部 ID/幂等语义；保留 run 与版本映射，读回校验，冲突人工处理，不以 case ID 覆盖跨运行结果 |
| 平台评估器误判或模型版本漂移 | 固定评估器版本并人工校准；保留本地关键失败；平台 LLM 分数保持 advisory |
| 用户内容或 Token 泄露 | 配置和有效授权双门槛，逐请求检查，缩减字段并脱敏；撤销停止后续上传、清理本地 pending；平台已接收数据按平台策略另行处理 |
| 平台故障影响本地体验 | Trace 故障不改变聊天结果；实验/数据集同步失败保留本地报告并允许经授权的同 run 重试；关闭对应平台开关后本地能力仍可用 |

**已确认的决策：**实验接入以目标 Workspace 实测为完成条件；独立命令消费已有 report/run 和已同步数据，不重跑 Agent。后续只需在 OpenSpec 中确定具体输入契约、重试与结果关联机制。

**仍需在各阶段 review 时确认：**

1. 阶段 B 的目标 Workspace、可用 API/权限及非敏感验收证据由谁提供；本轮文档默认均为待验证。
2. 无评测对象实验及结果查询的真实 API、评估器版本绑定、数据集版本快照与外部键语义；验证失败时是调整方案还是暂停 Step 10。
3. A/B 的对比样本、重复次数、质量/成本/延迟判定口径；校准审核人与允许误差；CI 的可信触发方式及非交互授权如何合规管理。

## 7. 文档 review 清单

- [ ] 确认 Step 1–12 全部属于本轮目标，但“仓库实现”和“平台验收”可分阶段完成。
- [x] 确认目标 Workspace 实测是实验接入完成的必要条件；无评测对象实验仍是待验证假设。
- [x] 确认实验提交先采用独立命令，复用已有报告与已同步数据，不重跑 Agent。
- [ ] 确认内容授权覆盖实验链路，平台评分不替代本地 Scorer/发布资格。
- [ ] 确认阶段 C/E 的 CLI 与 CI 方案必须先形成独立 OpenSpec 并再次 review；本文不授权修改代码。
- [ ] 确认 Workspace 证据、校准阈值和 A/B 判断仍是公开待决项。

待本文件 review 通过后，按阶段维护 OpenSpec 需求/设计/任务、逐阶段确认实现与验收；不要直接把本文的“计划”标记为完成。
