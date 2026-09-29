# Design

## Context

现有 `.github/workflows/evaluation.yml` 的 PR job 运行 Go 测试和离线 manifest 校验；当前非 PR smoke 的 `if: github.event_name != 'pull_request'` 也允许 `workflow_dispatch`，缺少受保护 Environment/ref 限制，且可能上传整个 `evals/reports/` 目录。该 live job 会将评测内容发送到模型 endpoint；模型请求授权与 CozeLoop 上传授权是不同边界。目前没有经批准的 CI 非交互模型请求/内容上传授权方式或 artifact 访问/保留政策。

本 change 的目标是安全地定义 Step 12。不能因 API Secret 存在就推断内容上传获授权，也不能在 workflow 中伪造本地交互 consent。授权机制和 artifact 治理未批准期间，只能运行离线部分及明确 skipped 的平台分支。

## Goals / Non-Goals

**Goals:**
- 普通 PR/fork 不依赖平台凭据，且不向不可信代码暴露 Secrets。
- 可信 live job 有明确触发、固定 Prompt/evaluator 版本、授权与失败分类。
- machine-readable 状态区分本地 execution、quality、live model request、CozeLoop platform、artifact upload 和 eligibility。
- 可在 runner 本地生成安全白名单投影；artifact 发布须待治理批准后开启，平台评分维持 advisory。

**Non-Goals:**
- 本 change 不制定或推断组织的 consent/隐私政策，不伪造授权，不开放未批准的上传。
- 不改变本地质量逻辑，不由平台分数取代本地 hard failure。
- 不将生产 Prompt 标签、部署端点或自定义 Agent 执行接入 CI。

## Decisions

### 1. PR 与可信任务隔离

普通 `pull_request`（包括 fork）job 只运行离线 Go tests、静态资产/manifest validate、fake tests 和脱敏投影测试；不读取 CozeLoop/model Secrets、不触发 live 模型请求或平台上传，不使用 `pull_request_target` 执行 PR head code。审批前必须关闭当前未受保护的 live smoke 与全目录 artifact 上传。获批后的 live/platform job 使用单独的受保护 environment、可信 ref 和经 review 的触发器；不能仅靠环境变量检查代替 GitHub Environment approval，`workflow_dispatch` 也必须限制为受保护 ref 并经人工批准。

### 2. 授权未批准时 fail closed 且可观察

现阶段没有已批准的非交互式授权。模型 endpoint 请求授权、CozeLoop 内容上传授权和 artifact 发布治理必须分别审批；不得将已有 CLI 交互授权复制到 CI、生成 synthetic consent 文件或把 Secret 存在误作 consent。审批前当前 live 模型 smoke、CozeLoop 上传和所有 CI artifact 上传均保持 disabled/skipped；不发内容请求、不上传文件，并输出准确状态与安全原因。审批后必须分别 review 授权主体、Workspace/API 绑定、有效期、撤销、可信 ref、artifact 访问者/保留期和 runner 清理规则。

### 3. 机器状态与退出语义

结果结构至少区分：
- `execution_status`：本地 runner 是否执行完成；
- `quality_status`：本地确定性规则与 Judge 的质量结果；
- `model_request_status`：live 模型 endpoint 请求的 disabled/skipped/completed/failed 状态；
- `platform_status`：CozeLoop 阶段的 disabled/skipped/submitted/completed/failed/incomplete 状态；
- `artifact_status`：artifact 阶段的 disabled/skipped/uploaded/failed 状态；
- `eligibility_status`：本地发布资格，永远不由 CozeLoop 成功覆盖。

CI 退出逻辑应由对应 job 负责，不以 `continue-on-error` 或 shell warning/exit 0 隐去本地质量失败或平台错误，也不把 execution、model-request、platform 或 artifact skipped 伪装成 quality pass。`execution_status`、`quality_status`、`model_request_status`、`platform_status`、`artifact_status`、`eligibility_status` 独立且稳定、机器可读。读取缺少新增状态的旧 report 时必须保留 `unavailable`/unknown，不得推断 remote 阶段通过或改变既有发布资格。可信 live job 必须记录 Prompt 解析状态，缺少固定版本、发生 fallback 或未显式启用 `--require-prompt-version` 时不得报告 eligible。

### 4. 安全 artifact 白名单

新增独立 projector 或等价显式序列化，只允许经过 review 的低敏字段。初始候选：run ID、commit、固定 Prompt/dataset/evaluator 版本引用、计数、各阶段独立状态、安全错误类别和受控证据引用。case 内容、回答、Prompt 原文/渲染文本、Tool arguments、Judge rationale、CozeLoop 原始 evaluator reason、fixtures、Token、HTTP body 一律禁止。输出使用独立目录/文件，不上传 `evals/reports/` 全目录。

artifact 访问者和保留期待用户/安全负责人决定；未定前不得上传任何 CI artifact，包括白名单摘要，更不得上传原始 report 或带私有 Workspace 内容的链接。白名单 projector 可以在 runner 上本地生成并测试，但 upload 必须 gated off 直到 artifact governance 获批。旧 report 仍留在受限本地存储。

### 5. 固定版本和平台 advisory

获批的可信 live job 必须显式固定 Prompt Version 与 evaluator immutable version，并传入 `--require-prompt-version`；固定版本解析失败或 fallback 时保留本地失败/无发布资格。模型请求授权与 CozeLoop 内容上传授权必须分别满足，平台实验仅消费 Step 10 已核验的运行快照。人工校准和阈值未获批准前，平台分数仅作为并列信息，不参加质量门禁。

### 6. 验证策略

- workflow 静态审查/可用的 YAML lint：检查 PR 不访问 Secrets、无危险触发器、可信环境隔离和 artifacts 路径。
- 本地单测：状态投影、白名单隐私 sentinel、状态/退出映射和授权缺失 skipped。
- CI 测试：无 Secrets PR 离线通过；未批准时 live 模型请求、CozeLoop 内容上传与 artifact 上传均 skipped/disabled；可信任务缺少任一授权时不发请求；平台失败不改变质量状态，且不得被 warning/exit 0 隐藏。
- 目标 Workspace：在模型请求和 CozeLoop 上传授权、可信 runner 均分别批准后跑最小固定版本任务；artifact 仅在访问/保留政策批准后上传并人工隐私审查。

## Risks / Trade-offs

- `continue-on-error` 模糊状态：拆分 job/机器状态，不以 warning 代替结果。
- PR Secret 泄露：彻底隔离 PR 和 trusted job，不在 privileged PR workflow checkout 执行不可信 head。
- report JSON 隐藏敏感字段：白名单投影与 sentinel 测试，不靠黑名单字段遗漏。
- 授权缺失导致无法完成 live CI：保留 live execution/platform/artifact disabled/skipped，并将其作为未完成的外部 gate，不伪造授权；模型请求、CozeLoop 上传和 artifact 治理各自单独表示。
- artifact 保留/访问未知：只可在 runner 本地生成并测试白名单摘要；政策确定前不上传任何 CI artifact。

## Open Questions

- 谁可以授权 CI 上传、授权如何绑定 Workspace/API、如何撤销及 runner 结束后清理？
- 哪些触发器可视作可信？是否只支持受保护分支/manual `workflow_dispatch`？审批人是谁？
- CI artifact 的允许访问者、保留期限与证据链接策略是什么？
- 哪些固定 Prompt/evaluator 版本和本地质量门槛由可信任务使用？阈值未批准前平台分数保持 advisory。
- 当前 advisory live smoke 的模型 endpoint 授权主体/非交互授权方案尚未批准；在此之前必须 disabled，不可仅移除整目录 artifact 上传后继续调用。
