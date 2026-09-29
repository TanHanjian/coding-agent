# Design

## Context

当前仓库已有本地 Runner、run/case/version/repeat 身份、CozeLoop 数据集同步及版本化 evaluator 资产，但没有实验领域 port、实验 adapter 或独立实验命令。现有 report 不持久化完整 Candidate 回答；Step 7 上传的数据集版本是实验输入的回答来源。现有同步逻辑不能证明 pinned version 仅包含本次 Run 的精确 item 集，也不能形成可信的 item ID 映射。目标 Workspace 是否支持无评测对象实验、如何将 `actual_output` 映射到 evaluator、如何稳定读取 item ID 及未知 POST 的恢复方式都尚未验证；报告的基础设施失败样本不得进入实验提交。

本 change 是 Step 10 接入，不是 `refactor-cozeloop-integration` 的后置结构优化。目标 Workspace 契约 PoC 是代码实现门槛；公开 IDL 和 fake HTTP 不可替代该门槛。

## Goals / Non-Goals

**Goals:**
- 从既有 report、同步状态、已核验数据集固定版本和固定 evaluator 版本生成实验请求；不重跑 Agent。
- 在提交前证明 live run、run-scoped 精确 item 集、版本和字段映射准确且授权有效；任一基础设施失败样本阻断整次提交。
- 为提交、未知结果、轮询、分页和逐 item 回关联定义可恢复状态。
- 只保存分数、状态和经白名单验证的脱敏原因码，不持久化原始 evaluator reason；保留本地评测为权威结果，平台状态仅作为附加状态。
- 人工校准可与技术 PoC 并行；校准完成前 LLM evaluator 分数只能作为 advisory，不可作为质量门禁或发布结论。

**Non-Goals:**
- 不改变本地评分、硬失败、报告质量结论或 Prompt 解析策略。
- 不新增 Agent endpoint、自定义评测对象、跨 Workspace exactly-once 或未经验证的自动恢复机制。
- 不实现 Prompt A/B、CI 集成或全链路代码重排。

## Decisions

### 1. Workspace PoC 必须先于实现

由获授权的操作方在目标 Workspace 使用非敏感 sentinel 数据验证：实验创建/详情/分页结果路由与权限；无评测对象模式是否真实运行；Code `actual_output` target-output 映射及每个 LLM evaluator 的输入字段映射；固定数据集/evaluator 版本引用；每个 Run 的 run-scoped pinned version 是否可创建/读取、是否精确包含该 Run item 集、唯一 item ID 与完整身份；请求响应丢失后的查找/重复语义。PoC 至少覆盖两个独立 Run、重复执行、部分写入重试，并核对每个 pinned version 都没有额外/缺失/重复项。对 Code 和每个目标 LLM evaluator 使用互不相同的非敏感 sentinel，核对实际分数、状态与固定脱敏原因码，不以创建成功/HTTP 2xx 代替字段映射验证。记录脱敏请求形状、资源版本引用及结果摘要，不记录 Token、Prompt、回答或原始 evaluator reason。

10–15 case 人工校准不阻塞上述传输/身份关联 PoC；在校准完成前任何 LLM 分数只作 advisory。若关键映射或安全恢复条件不成立，停止 Step 10 的实现/上线并更新本 spec；不得改用部署 Agent Endpoint 或仅凭 2xx 认定成功。

### 2. 实验只消费已同步且可核验的运行快照

输入包括已有 live report、与其同 run 且 Workspace/API scope 可核验的 `CozeLoopSync` 固定 dataset/version 引用、及已发布 immutable evaluator 引用。Dataset version 必须是当前 Run 的 immutable snapshot，且其 item 集与该 Run 完全相同；共享 dataset ID 或 sync 成功状态本身不构成 run-scope 证明。若现有 Step 7–9 sync 版本混有其他 Run 的 item，必须先由已验证的同步流程产生/选定精确的 run-scoped version；在创建路径经 Workspace PoC 确认前，Step 10 MUST fail closed，不得提交混合版本实验。只接受完整记录 `run_id + case_id + case_version + repeat_index` 的 live report；任何缺失身份、版本推断、重复身份、非 live Run 或基础设施失败样本均在创建实验前拒绝整次提交。report 中的身份集合须与从 pinned dataset version 回读的完整 item 集精确一致，必须拒绝额外/缺少/重复 item，并读取唯一 `item_id -> execution identity` 映射。report 不用于重建 Candidate 回答。若平台省略内容，且无法以经 Workspace 验证的读取方式核验实际 item 内容和身份，则 fail closed；远端 `sync_content_hash` 本身不能替代内容回读。

### 3. 独立领域流程、适配器和命令

在 `backend/internal/eval` 建立最小实验请求/状态/结果模型与窄 `ExperimentPlatform` port；CozeLoop 路由及 wire DTO 只放在 `backend/internal/infrastructure/cozeloop`。新增独立命令（建议 `backend/cmd/cozeloop-experiment`），不把实验流程放入现有 `cmd/eval` 或评估器发布 CLI。建议命令形状：

```text
cozeloop-experiment submit --report <path> --evaluators <manifest> --apply [--wait]
cozeloop-experiment reconcile --report <path> --experiment-id <id>
```

`--evaluators` 使用现有本地资产 manifest；另需显式 run-scoped `--evaluator-refs <path>` 映射本地 evaluator key 到 `workspace_ref + evaluator_id + immutable_version_id + content_sha256`。提交前必须从目标 Workspace read-back 验证引用、发布状态、内容哈希与 Workspace scope；manifest 的本地语义版本不等同远端不可变版本，禁止 latest/tag/draft。命令名和其他参数须在实现任务前与当前 CLI 约定核对；所有写请求需显式 `--apply`。未确认参数、版本和 Workspace 行为前不创建客户端或发送写操作。

### 4. 提交状态与本地恢复记录

使用与 run 绑定的受限侧车记录最少化元数据，例如 schema version、Workspace 引用、run ID、dataset/evaluator 固定版本、规范化意图指纹、已知 experiment ID、状态、item ID 到执行身份映射及安全错误类别。不得存储 case 原文、回答、Prompt、工具参数、Token 或未经脱敏的平台错误。

状态至少区分 prepared、submitted/running、unknown、completed、failed 和 incomplete/reconcile_failed。侧车只保留 allowlist 分数/状态与固定脱敏原因码，不写入 CozeLoop 返回的自由文本 `reason`；若远端 evaluator 无法保证固定、非回显的 reason 输出，客户端仍必须丢弃原文且不写日志。未知结果只读恢复；不自动重发写请求。文件需限定在该 run 目录、拒绝路径逃逸/链接替换、原子更新并遵守当前 Windows/Unix 受限权限策略。

### 5. 内容授权按实际请求载荷判断

不得仅凭 HTTP 方法或端点名称判定请求为 metadata-only。任何实际携带完整评测内容的请求都在发送前重新检查评测功能开关、内容上传开关与当前匹配 Workspace/API 范围的授权。撤销后停止 pending 重试。公开错误仅包含安全分类，不回显响应原文。

### 6. 结果和状态的完整性

轮询有总时限、退避上限、分页页数/条数上限和取消传播。所有页面都需读完并校验总数、唯一 item、item 身份及 evaluator 输出。未知 item、缺失/重复 item、缺失 score/status 均标记不完整；自由文本 reason 不作为完成条件且不得持久化，只能保留 allowlist 中的脱敏原因码。CozeLoop aggregate 仅作展示，不能替代逐项结果核对或更改本地质量判断。

### 7. 验证分层

- 单测验证身份、精确集合、固定版本、状态转换和安全投影。
- Fake HTTP 验证公开契约、DTO、分页、超时/取消、重定向与授权复核；不代表 Workspace 支持。
- Workspace smoke 验证真实路由、权限、字段、固定版本、item 回关联、结果分页和恢复行为。
- 全量 Go tests、vet、离线命令校验和 `git diff --check` 用于本地回归。

## Risks / Trade-offs

- Workspace 不支持无评测对象模式或字段映射不成立：停止实验能力，回到方案 review，不自动切换 Agent Endpoint。
- 无法为当前 Run 取得精确的 immutable dataset snapshot：拒绝提交，先 review Step 7–9 sync 的 run-scope 职责，避免跨 run 污染。
- Item ID 或快照不可核验：拒绝提交，避免跨 run 污染。
- 提交响应丢失：以 unknown 停止盲重试，不承诺 exactly-once。
- 基础设施失败样本：整次实验提交前置校验失败，不产生实验写请求，避免状态被 evaluator 误判为质量分数。
- evaluator reason 可能回显输入：测试资产只输出固定脱敏原因码，客户端丢弃所有自由文本 reason。
- 授权撤销发生在多次请求之间：逐请求重查授权，撤销后不再发起内容请求。
- 新侧车格式与旧报告共存：旧 report 保持只读兼容；侧车格式另设版本且失败不得覆写 report。

## Open Questions

- 目标 Workspace 是否支持不设置 evaluation target 的实验，且 evaluator 能从数据集字段读取 `actual_output`？
- Workspace 是否能创建/读取 run-scoped immutable dataset version，返回可核验的精确 item ID/完整 item 集，且结果可按 item ID 回查？现有 Step 7–9 sync 是否已经产生这种版本，仍需 PoC 确认。
- 真实 Workspace 的未知 POST 恢复查询能力和重复语义是什么？
- `--evaluator-refs` 的具体 JSON Schema 和 Workspace immutable version read-back 路由，需在 PoC 后按实际平台契约冻结；语义必须包含 Workspace scope、远端 evaluator ID、不可变版本 ID 和内容 hash。
- Workspace smoke 的操作人和非敏感证据存放位置待指定。
