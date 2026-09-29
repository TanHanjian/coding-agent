# Design

## Context

见 [proposal.md](proposal.md) 和 [全链路代码优化方案](../../../docs/cozeloop-code-optimization-plan.md)。截至规划时，Step 1–9 具备仓库实现但目标 Workspace 验收仍待完成；Step 10–12 的实验/A/B/可信 CI 尚未实现。当前可见的交叉点包括：`backend/internal/infrastructure/config/config.go` 的集中配置，`infrastructure/cozeloop/client.go`/`tracing.go` 的 SDK 与进程级 Handler，`prompt_provider.go`/`agent/prompt` 的 Prompt 选择，`eval/run_observability.go` 的评测上下文，`eval/dataset_sync.go`/`evaluator.go` 的独立领域 port，`infrastructure/cozeloop/evaluation_client.go`/`evaluator_client.go` 的 HTTP DTO，以及 `cmd/eval/main.go` 的运行、报告、pending 与续传编排。

本设计是**接入完成后的重构方法**，不是这些未来模块已存在的声明。实施前须先复核所有 Step 1–12 的实现、操作路径、目标 Workspace 证据及功能开关。数据泄露、错误结果对齐或无授权上报等阻断项必须在相应接入验收前由原接入 change 修复；不以本 change 的后期任务代替其验收。

`.openspec.yaml` 声明 `skip_specs: true`：本 change 保持已批准的对外行为，不新增或修改能力需求。行为基线来自当时已完成的 OpenSpec 与真实 Workspace 验收。若需要改变公开报告字段、命令/退出码、持久格式、CI 触发/权限、上传授权或 Prompt 解析语义，先另立相应的 delta spec 和迁移评审，暂停本 change 的有关切片。

## Goals / Non-Goals

**Goals:**

- [Phase 3] 为全部 CozeLoop Step 1–12 代码划清配置/装配、Prompt、观测、评测用例、平台适配和数据安全责任，使调用方无需知道跨文件的内部操作顺序。
- [Phase 3] 去除有证据的重复解释或转换，保证身份、固定版本、授权和状态含义一致，保持现有的本地权威与平台 advisory 分层。
- [Phase 3] 通过每片行为基线、故障注入、fake HTTP 和受控 Workspace smoke 验证重构；保留旧报告/pending 可读与离线运行能力。

**Non-Goals:**

- 不实现尚未交付的实验、A/B 或 CI 能力；不把不满足验收条件的功能伪装成重构已完成。
- 不为了文件整齐引入跨所有 CozeLoop 能力的万能 Manager、通用请求框架或假想 port。
- 不在此 change 修改内容授权范围、外传字段、平台门禁或发布决策。

## Decisions

### 1. 基线先于结构：按行为清单冻结，不凭当前文件树假设未来

**选择：**在任务 0 建立基线清单：Step 1–12 各命令/开关、输入输出和退出行为、已核验的 Prompt/Trace/评测集/评估器/实验/A/B 关联、授权撤销边界、报告与 pending 版本、CI 可信条件、已知失败恢复。每项对应测试或受控 Workspace 证据；比较重构前后输出而不是比较内部函数调用。缺少验收证据时停止本 change，不以 fake server 替代。

**原因：**Step 10–12 尚未完成；现阶段直接规定其函数或文件迁移会误锁定不存在的实现。**替代方案：**先建一套新目录再迁移所有代码，容易使历史 pending、SDK 回调和实际平台字段失配，故不采用。

### 2. 让配置/装配与进程级 Trace 拥有单一清晰的生命周期

**选择：**在装配入口统一解释共享端点/凭据和按能力的独立开关，向 server、eval、evaluator CLI 及未来实验命令提供所需依赖和关闭职责；保留各自受限的 port/adapter。Eino 进程级 callback 注册规则由装配层明示；不同 Client/配置重复注册要由原验收行为约束，无法等价替换时以显式错误/独立进程测试代替隐性覆盖，若会改变对外行为则另立 spec。观测上下文抽成与 eval 语义解耦的最小合同，评测仅负责注入 run/case/repeat 关联字段。

**原因：**现有 server 与 eval 重复建 SDK Client、Handler、Prompt Provider 并清理，`registerGlobalOnce` 会绑定首次 Client；当前 `tracing.go` 还直接依赖 `eval.TraceCapture`。**替代方案：**把所有能力迁入唯一 CozeLoop Client，看似减少装配却让 Prompt/Trace/Evaluation 生命周期捆绑，拒绝。

### 3. 按可观察不变量收拢 Prompt 解析和身份/字段投影

**选择：**将 Version/Label 优先级、requested/resolved/source/fallback、内容 hash 的现有口径集中在 Prompt 解析 seam；保留 strict eval 与在线 fallback 两条已验收策略。把 `run_id + case_id + case_version + repeat_index` 的唯一性及校验入口集中，并以经核验的字段目录生成数据集上传和评估器最小化输入。实验只消费已确认的 item 绑定，A/B 只消费完整可比的 run 引用。所有映射依然经当前授权条件与内容净化检查；HTTP DTO 留在平台适配层。

**原因：**数据集 schema 与写入字段分别定义，评估器 Debug 通过数据集字段再二次映射；CLI、Provider、评测包装器分别处理 Prompt 选择/审计。**替代方案：**直接序列化整个 `EvalCase`/`CaseResult` 到平台，存在扩大内容外传的风险，拒绝。若调整 hash 语义或字段集会改变持久/远端结果，必须先获单独 spec 批准，而不是借重构变更。

### 4. 用窄用例隐藏远端流程和失败恢复，保留独立 port

**选择：**本地 Run、同步评测集、发布评估器、提交/续查实验、A/B 对比各有自己的用例编排；CLI 负责参数、装配、结果/退出状态映射。用例封装已验收的状态转换、幂等检查及受限文件读写，调用现有/后续的窄 port；只有真正由多个适配器共享的基础设施约束（安全请求、分页、错误脱敏、私有存储）才抽公共实现。复用必须以保留数据集、Validate/Debug、实验的**不同内容敏感级别**为前提。

**原因：**`cmd/eval/main.go` 直接保存 pending、写报告、同步、撤销清理和判断错误字符串；`cmd/cozeloop-evaluators/main.go` 与 `eval.EnsureEvaluatorVersion` 分别编排发布。**替代方案：**把所有端点塞进一个 `Platform` 大接口，会将变更放大到全部调用方和 fake，拒绝。

### 5. 三类产物和类型化错误服务于已验收语义

**选择：**区分受限本地运行原件、授权待同步载荷、可分享/CI 摘要；每种产物有自己的写入/读取/保留责任。类型化错误只替换内部文本判定，覆盖配置、授权撤销、身份冲突、内容不可核验、权限、传输、远端写结果不明、结果不完整，最终 CLI 用户可见分类/退出码按基线不变。对报告/待同步 payload 保持当时已验收的 schema、迁移与清理行为；必要的持久格式/可见内容改变必须另立 spec。

**原因：**当前撤销通过错误字符串驱动 pending 删除；`consent.go` 同时持有授权记录与评测 pending 存储；未来实验又引入 unknown 提交结果。**替代方案：**通过捕获完整平台响应增强诊断易泄漏凭据或用户内容，拒绝。

### 6. 测试从接口观察行为，性能改动先基准后优化

**选择：**每片先为外部行为补回归/故障测试，再改实现，再运行同一组测试和局部 Workspace smoke。fake port 证明用例状态；fake HTTP 验证适配器 DTO、授权、分页/重试；目标 Workspace 验证平台行为。进程级 Handler 通过可注入装配或进程隔离测试，不靠顺序敏感的全局变量重置。对 PromptFormat、Graph 构建、授权文件读取和 Usage/Trace 聚合先测量，再决定是否引入缓存或其他性能改动；缓存不得改变固定版本和撤销语义。

**原因：**重构不能以单测通过宣称平台已验收，也不能靠未经测量的缓存增加身份/权限复杂度。

## Risks / Trade-offs

- [验收基线缺失，尤其 Step 10–12 尚未实现] → 停止 R0 之后的重构；先完成对应接入与目标 Workspace 实测，不把推测写成测试期望。
- [改动期含用户内容或已授权数据泄露] → 在同一测试输入中注入敏感工具参数、Prompt、Judge 理由和错误文本；本地原件、pending、摘要、CI、日志各按已验收的可见性比较。
- [共享 transport 抹平不同授权要求] → 对发送真实数据的每类请求分别保留双门控/撤销回归；资产管理接口也审查是否携带 Prompt 内容。
- [统一版本/字段目录误改对外数据] → 逐字段、逐版本 golden 测试，构造跨 run/repeat、空 Usage、缺失结果与 content_omitted 正反例；差异不能静默更新快照。
- [全局 Eino Handler 难以在同一进程可靠替换] → 不承诺动态替换；用进程级装配约束和隔离测试检验实际生命周期，需要变更则单独 spec。
- [切片跨越过多上下文或与现有未提交改动冲突] → 一次只迁移一个窄用例，先冻结基线，保留 Step 8–9 当前工作树，不以重构回滚其他修改。

## Migration Plan

1. **R0 基线/守门：**检查 Step 1–12 和 Workspace 验收（特别实验无 target、item 关联、A/B 与 CI 授权），建立回归矩阵；所有接入期阻断问题先在所属 change 中处理。任何缺项均保持本 change 未启动。
2. **R1 数据与隐私：**先补当前已验收的分级产物与字段/授权契约测试，再收拢重复的数据投影/存储责任；不改变白名单内容。逐片跑 Go 回归和本地报告兼容测试。
3. **R2 身份与状态：**统一身份校验、内部错误类别和同步/实验恢复编排，旧报告/pending 可读、写入成功/失败/撤销清理与外部退出状态不变；故障注入测试后再迁移 CLI。
4. **R3 装配/Trace：**收敛能力装配与 Close 所有权，验证重复注册、不同配置、流式回调与 Candidate/Judge Trace；保留禁用配置下的纯本地运行。
5. **R4 Prompt：**收敛解析/审计口径，固定版本/缓存/在线 fallback/strict eval 的对照样本全部回归；出现持久字段语义变化则暂停并走独立 spec。
6. **R5 评测与平台用例：**逐个迁移数据集、评估器、实验、A/B；对每个接口分别做 fake port/HTTP 和 Workspace 小规模对照，不整体切换。
7. **R6 CI/文档：**确认现有 PR/可信任务触发条件、Skip/失败分类、报告可见性与验收记录都不退化，收敛操作文档与安全摘要生成责任。

每片允许仅内部回滚到重构前实现；已上传数据和已发布评估器/实验不可因代码回滚而删除。一个切片出现报告格式、身份或远端内容差异即停止后续切片并复原该片；先评审是否属于预期的行为变化。整项完成的证据是旧/新行为对照、Go tests/vet、fake HTTP、受控 Workspace smoke 和单独的隐私检查，而不是文件减少数量。

## Open Questions

- Step 10–12 最终实现的模块与操作细节尚未出现；R0 会用验收后的代码替换本文的示意路径和回归样本。此项不改变“先基线、再按职责切片、保持行为”的设计，但会决定各切片的具体文件集合。
- 现有 Eino Callback API 是否允许在不改变业务行为的条件下测试局部注册？若不允许，继续用进程隔离而不引入请求级替代方案。
