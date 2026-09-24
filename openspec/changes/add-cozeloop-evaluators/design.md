# Design

## Context

See `proposal.md` for motivation and `specs/cozeloop-evaluators/spec.md` for externally visible requirements.

当前仓库已有两种本地评分能力：`backend/internal/eval/scorer.go` 保留工具行为和答案规则断言，`backend/internal/eval/judge.go` 输出忠实度、指令遵循、完整性和可执行性四项 0–4 分。本地 runner 将实际回答、执行状态、参考事实、禁含内容、Tool Trace、候选/Judge Trace 与 Usage 写入报告；Step 7 的数据集映射也已覆盖其中多数字段。

`backend/internal/infrastructure/cozeloop/evaluation_client.go` 当前只实现评测集操作，并把内容授权、HTTP 安全和凭据过滤放在 adapter 内。评测 CLI 的 `backend/cmd/eval/main.go` 及多个相关实现文件当前有未提交改动；本 change 不重写或回滚这些修改，优先使用独立命令入口。

公开 IDL 在 [coze.loop.evaluation.evaluator.thrift](https://github.com/coze-dev/coze-loop/blob/f27d2fb2/idl/thrift/coze/loop/evaluation/coze.loop.evaluation.evaluator.thrift) 和 [domain/evaluator.thrift](https://github.com/coze-dev/coze-loop/blob/f27d2fb2/idl/thrift/coze/loop/evaluation/domain/evaluator.thrift) 定义了 evaluator 创建、草稿更新、版本提交/查询、Validate、Debug/BatchDebug、Run 等操作，以及 Code/Prompt evaluator 内容和输入/输出 DTO。它说明公开服务端契约中存在这些接口，但不证明目标 Workspace 已开放接口、权限或相同字段行为。

## Goals / Non-Goals

**Goals:**

- 用版本化本地清单描述四个基础 Code evaluator、可选 Tool Trace evaluator 和四个独立 LLM 维度。
- 通过 `eval` 领域 port 与独立 CozeLoop evaluator adapter 隔离平台 DTO；适配公开 IDL 中的创建、草稿更新、验证/调试和版本提交/查询操作。
- 允许对声明的有效样例进行远程 Debug，并在本地记录精确的 evaluator key/version 与结果。
- 在不改变本地评分语义的前提下，为 Step 10 的实验提交保留可查询的 evaluator 名称和固定版本。

**Non-Goals:**

- 本 change 不执行或宣称完成目标 Workspace 的在线验收。
- 不把平台 LLM 分数升级为 CI 门禁，不删除本地 scorer/Judge。
- 不自动创建评测实验、不实现 Prompt A/B，也不迁移到自定义评测对象。

## Decisions

### 1. 独立领域 port 和 evaluator REST adapter

在 `backend/internal/eval` 增加 evaluator 规格/结果模型与 `EvaluatorPlatform` port；在 `backend/internal/infrastructure/cozeloop` 实现该 port。adapter 按固定公开 IDL 路径构造请求，并将原始 DTO 留在基础设施层。至少覆盖 List/Create/UpdateDraft、ListVersions/SubmitVersion、Validate 和 BatchDebug；本期不实现删除接口。BatchDebug 的公开 IDL timeout 为 300 秒，因此 evaluator client 的请求 timeout 设为 5 分钟，避免通用 30 秒超时截断合法调试请求。

这样不扩张当前只管理评测集的 `EvaluationPlatform` 业务接口，也不把 CozeLoop 原始 DTO 暴露到评分器。请求继续遵循现有 API Base URL、HTTPS/loopback 限制、同源重定向限制、响应体上限和不回显凭据/原始错误等安全约束。

### 2. 声明式 evaluator 资产与精确版本

将本地资产放在 `evals/cozeloop/`：清单记录逻辑 key、名称、类型、固定版本、描述、输入/输出 schema 和资产文件引用；Code 源码及四份 LLM rubric/prompt 独立存放并纳入版本控制。对每个资产计算规范化内容摘要，用于确认本地定义与远端草稿/已发布版本一致。

CozeLoop [自建评估器文档](https://docs.coze.cn/cozeloop_create_evaluators) 定义 Code 测试数据包含 `evaluate_dataset_fields`、`evaluate_target_output_fields` 和 `ext`；[Code 评估器实践教程](https://docs.coze.cn/cozeloop_code_evaluator_and_experiments) 展示 Python 入口 `exec_evaluation(turn)` 以及 `EvalOutput(score=..., reason=...)` 返回形式。LLM Prompt 资产按 [Prompt 文档](https://docs.coze.cn/cozeloop_prompt) 使用 Normal 模板的 `{{variable}}` 占位符。资产按这些公开协议实现，并将结果理由映射到 CozeLoop 评估结果中的理由字段。

Code 资产至少独立实现：非空回答、must-contain、must-not-contain、执行状态；Tool Trace 结构 evaluator 可选。为与非空 Code evaluator 保持同义，本地 `ScoreDeterministic` 对空回答新增不占用 60 分配比的 critical hard-check。must-not-contain 输入包含 case 的普通禁含与 critical forbid 字段，但本地仍保留两者区别及关键失败判定。LLM 资产沿用当前 Judge 的四维定义和 0–4 分语义，但每个维度独立提示、独立版本和独立理由。不得将工具选择、工具参数或 Graph 内部关键规则从本地 scorer 搬走。

远端资源通过 workspace 内稳定且 namespaced 的名称定位；创建前精确过滤名称和类型，重名或类型不符时失败，不自动覆盖或删除。版本提交使用清单中的具体版本；若同名版本已存在，须读取并比较内容摘要，一致则复用，不一致则作为冲突失败。远端 ID 不写入 Git 或日志中的敏感字段，CLI 输出只提供后续人工记录/排查所需的非密钥标识。

### 3. 独立 CLI，避免与 Step 6/7 未提交入口冲突

新增独立命令 `backend/cmd/cozeloop-evaluators`，不在本 change 修改现有 `backend/cmd/eval/main.go`。`validate` 只本地校验，可选按 key 筛选；远程 `debug` 和 `publish` 每次只处理一个 evaluator，必须显式提供 `--key` 和 `--inputs <json-file>`。`--inputs` 是调用方本地准备的 `[]EvaluatorInputData` JSON 文件，应包含已有 Candidate 输出；不从不含实际回答的持久化 report 猜测输入，也不重跑 Agent。远程 `debug` 先调用 Validate，再对同一输入集合调用 BatchDebug。`publish --apply` 在任何远端元数据创建/修改前，必须先通过 Validate 和 BatchDebug；缺少 `--apply` 时在加载 CozeLoop 配置或创建客户端前拒绝。发布前先检查固定版本：内容哈希相同则复用、不更新草稿；同版本内容冲突则停止。错误必须区分本地定义错误、平台权限/API 错误和 Workspace 待验证状态，并过滤 API Token。

### 4. 调试输入与授权分层

调试数据基于已选 EvalCase 和本地 Candidate 输出构造 `EvaluatorInputData`：仅映射当前 evaluator manifest 声明的输入字段，并按公开 IDL 的 Content 结构生成 `{content_type: "Text", text: ...}`；optional 输入 schema 使用空 Text `default_value`，缺省字段也显式传空文本。LLM Prompt 变量仅放入 `input_fields`，Code 输入按 `evaluate_dataset_fields` 与 `evaluate_target_output_fields` 分组；`actual_output` 明确来自 Candidate 并映射到 target output，不调用 Agent 再生成回答。问题、面试材料、参考事实、合并后的禁含规则、执行状态和 Tool Trace 均按各资产声明映射；Tool Trace 仅保留工具名称、空 `arguments` 对象和脱敏错误，不上传 arguments 实际值或其他未声明字段。不在 `ext` 上传 case/run/repeat 标识，依靠保持的 batch 顺序关联输入和输出。基础设施失败样本从 batch 中排除，其他有效样本保持报告原顺序。

评估器定义管理只发送 evaluator 代码、rubric、schema 和名称，不发送 case 内容。任何 Validate/Debug/Run 请求若包含完整 case 或实际回答，必须在每次发送前同时检查 `COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED=true` 和当前有效授权；授权撤销后立即阻止后续内容请求。Validate 请求提供 `input_data` 后，若返回 `valid=true` 却缺少 `evaluator_output_data`，视为不完整响应；BatchDebug 每个成功结果必须含 score 和非空理由，执行错误仅保留安全分类，不回显平台错误消息。继续使用现有内容授权记录和脱敏规则，不将凭据写入 manifest、pending payload、日志或报告。

### 5. 把平台验收作为独立门槛

本地 fake server 覆盖 IDL DTO、endpoint、精确版本和错误/授权行为；这只证明 adapter 依据固定 IDL 的本地映射。目标 Workspace 实测应另行确认 API 可达性与权限、code evaluator runtime/schema、LLM evaluator 的模型配置、字段展示映射、版本提交及返回分数/理由。`evals/cozeloop/acceptance.md` 必须保留 `workspace_verification=pending`，只有人工记录目标 Workspace 证据后才允许标记通过。

校准资产记录 10–15 个 case 的 case/version、四个维度的人工评分、CozeLoop 评分、差异和解释。校准完成前维持 advisory，不修改本地 `ReleaseEligible` 或 CI 结论。

### 6. 验证策略

- 本地：清单/schema/版本摘要测试；Code 资产的本地精确行为测试；LLM rubric 输出契约测试；字段映射测试。
- Fake HTTP：覆盖创建/查找/更新草稿/提交固定版本/版本复用和冲突、Validate/BatchDebug DTO、API 错误脱敏、缺授权拒绝、授权撤销的每次请求复核、超时与取消传播。
- Workspace：手工验证 4 个 Code evaluator 及 4 个 LLM evaluator 的创建、调试、发布版本和字段映射；选择 10–15 个 case 做人工校准。此项状态独立于本地自动化测试。
- 回归：`cd backend && go test -mod=readonly ./...`、`cd backend && go vet ./...`、`git diff --check`。不运行前端应用。

## Risks / Trade-offs

- [公开 IDL 与目标 Workspace 不一致] → 只将 IDL 当作实现参考；通过 Workspace 手工验证权限、字段和实际响应，未验证前保持 pending。
- [公开文档协议与 Workspace 的 Code runtime 行为有差异] → 资产使用已文档化的 `exec_evaluation(turn)` / `EvalOutput(score, reason)` 协议；runtime 可用库、实际字段映射及 Validate/Debug 行为仍须在目标 Workspace 验证，失败时阻止发布，不将 fake 测试视为运行通过。
- [LLM evaluator 的模型配置依赖 Workspace 可用模型] → 不硬编码 Workspace 模型 ID；在远程验证时显式选择/检查模型配置，不能静默换模型。
- [版本提交或请求重试可能产生重复/冲突资源] → 使用稳定名称、固定版本和内容摘要；已存在的同版本必须校验后复用，不一致则停止。并发 `publish` 可能同时通过“版本不存在”的预检查；公开 IDL 未提供本地可用的 compare-and-set 保证，因此不得并发发布同一个 evaluator。提交后必须读回验证；Workspace 验收还需检查服务端版本唯一性行为。
- [评估内容可能含用户学习材料] → 内容操作逐请求检查配置和授权；凭据与错误始终过滤；撤销授权后不重试或继续上传。
- [平台与本地断言存在差异] → 本地关键规则仍是确定性基线；列出差异 case 和原因，平台 Code evaluator 不参与本地 pass/fail 决策。

## Migration Plan

1. 新增 evaluator manifest、Code/Prompt 资产、映射与独立 adapter/CLI；不改本地 case schema，除非实现发现某字段确实无法表达，届时先更新 spec。
2. 使用本地 `validate` 和 fake server 测试资产与请求映射，不需要 CozeLoop 凭据即可完成离线检查。
3. evaluator 定义管理使用 `COZELOOP_EVALUATION_ENABLED`；在用户显式授权且配置内容上传后才能执行远程 Debug。只有调试成功的 evaluator 才允许显式 `publish --apply` 固定版本。
4. 将人工 Workspace 验收和校准记录为独立状态；失败时可停止 evaluator CLI，既有本地 Runner、报告和 dataset JSONL 不受影响。

## Open Questions

- 目标 Workspace 是否开放以上 evaluator routes、当前 token 具备哪些权限，以及账号是否支持所需的 Code runtime 和 LLM model config，需在独立 Workspace 实测中确定。
- Code 入口与 `turn` 字段结构已有官方文档示例；目标 Workspace 的 runtime 可用库、schema 解释和 Validate/Debug 错误行为仍须实测。若与本 spec 或已文档化协议不兼容，先修订 spec/资产后再发布。
