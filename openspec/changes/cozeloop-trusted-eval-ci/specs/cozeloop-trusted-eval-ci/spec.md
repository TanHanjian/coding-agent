# Spec Delta

## Purpose

定义 CozeLoop 可信评测 CI 的离线 PR 边界、平台授权、失败状态和产物隐私，确保普通 PR 不依赖平台，平台失败不混淆本地质量结论。

## ADDED Requirements

### Requirement: 普通 PR 离线与 Secrets 隔离
[Phase 3] 普通 PR（包括 fork）MUST 仅运行不需要 CozeLoop/模型 Secrets 的离线测试、资产校验和报告安全检查。PR 工作流 MUST NOT 将 CozeLoop 或模型凭据暴露给不可信代码，不得发起 live 模型请求、CozeLoop 内容请求或 artifact 上传，也 MUST NOT 使用 privileged PR 触发器执行不可信变更并读取这些 Secrets。

#### Scenario: 普通 PR 无平台凭据
- **WHEN** PR/fork 工作流在无 CozeLoop/模型 Secrets 的环境运行
- **THEN** 离线验证 MUST 可完成，且不得连接 CozeLoop、发送 live 模型请求、上传 artifact 或因缺少平台凭据而失败

#### Scenario: PR 尝试触发平台上传
- **WHEN** 普通 PR 请求启用 live evaluation 或 CozeLoop 上传
- **THEN** 工作流 MUST 拒绝或安全跳过该阶段，不得加载平台 Secrets 或发送评测内容

### Requirement: 可信任务的明确授权门控
[Phase 3] live 模型请求与 CozeLoop 平台任务 MUST 分别获得非交互请求/内容授权，并且只可在经批准的可信触发条件与受保护环境内运行。每个实际携带评测内容的请求 MUST 同时具备对应凭据、功能开关和有效授权；模型 endpoint Secret 不等同于 CozeLoop 内容上传授权，任何平台 Secret 也 MUST NOT 被当作内容授权。MUST NOT 在 CI 自动构造/伪造用户 consent。当前授权主体、方式、可信触发条件或撤销策略未获批准时，对应 remote 请求 MUST 保持 disabled/skipped。

#### Scenario: Secret 或授权缺失
- **WHEN** 可信任务缺少对应模型/API Secret、有效授权、Workspace/API scope、可信 ref 或批准的授权策略
- **THEN** 对应 live 模型或 CozeLoop 平台阶段 MUST 标记 disabled/skipped，且 MUST NOT 发送含内容的请求；本地报告和质量状态仍可生成

#### Scenario: 授权撤销
- **WHEN** 授权在一次请求之后被撤销
- **THEN** 后续上传与 pending 重试 MUST 被阻止，并记录不含内容的安全状态

### Requirement: 各阶段状态与质量结果分离
[Phase 3] CI 结果 MUST 使用稳定、机器可读的 `execution_status`、`quality_status`、`model_request_status`、`platform_status`、`artifact_status` 和 `eligibility_status`，分别表达本地 execution/quality、live model request、CozeLoop platform、artifact upload 与发布资格。模型请求、平台或 artifact 阶段 disabled/skipped MUST NOT 被映射为 quality pass，也 MUST NOT 覆盖本地 hard failure、Judge 或 `ReleaseEligible`。未经校准及单独批准的平台分数 MUST 保持 advisory。

#### Scenario: 本地质量失败且平台成功
- **WHEN** 本地评测产生 hard failure，但 CozeLoop 实验成功
- **THEN** CI MUST 保留 `quality_status=failed` 和 `eligibility_status=ineligible`，平台成功只能作为独立状态

#### Scenario: 本地质量通过但平台失败
- **WHEN** 本地质量通过而平台提交/查询失败
- **THEN** CI MUST 同时表达 `quality_status` 与 `platform_status=failed`，不得将两者折叠为单一“通过”

#### Scenario: Live/platform/artifact stages 未获批准
- **WHEN** 模型请求、CozeLoop 上传或 artifact governance 任一未获单独批准
- **THEN** 相应 `model_request_status`、`platform_status` 或 `artifact_status` MUST 为 disabled/skipped；本地离线 execution 和 quality 状态独立保留，且不得把跳过解释为验证通过

#### Scenario: 可信 job 失败
- **WHEN** 本地质量或已批准的可信 remote job 失败
- **THEN** workflow MUST 以失败状态退出；MUST NOT 通过 `continue-on-error`、shell warning 或 exit 0 隐藏失败

### Requirement: 固定 Prompt 与 evaluator 版本
[Phase 3] 获批的可信 live job MUST 固定 Prompt Version 与 CozeLoop evaluator immutable identity/version，并显式启用 `--require-prompt-version`。Prompt 解析缺失、固定版本不匹配或发生 fallback 时 MUST 标记 Prompt/version validation 失败且不得 eligible；PR 离线 job MUST NOT 因该 live gate 调用模型或 CozeLoop。

#### Scenario: 可信 job Prompt 版本缺失或 fallback
- **WHEN** 获批的可信 job 缺少固定 Prompt Version、解析到不同版本或执行 fallback
- **THEN** job MUST 保留错误/非 eligible 状态并按可信 job 失败退出，不得将本地 Prompt fallback 标成 release eligible

### Requirement: 白名单安全产物
[Phase 3] 在 artifact 访问者与保留期政策获批前，CI MUST NOT 上传任何 artifact（包括白名单摘要）。获批后才可由显式白名单投影生成 artifact，且只包含经批准的 run/版本引用、汇总计数、状态、安全错误类别和允许访问的受控证据引用。MUST NOT 直接上传包含原始回答、Prompt、工具 arguments、Judge 理由、CozeLoop 原始 evaluator reason、fixture、凭据或未脱敏平台错误的 report 目录。

#### Scenario: artifact 治理已批准且输入报告含敏感字段
- **WHEN** artifact 访问者与保留政策已获批准，且原始 report 包含真实回答、Prompt、工具参数、Judge 理由、CozeLoop 原始 evaluator reason 或平台错误文本
- **THEN** 上传的 CI artifact MUST 通过白名单投影排除这些字段，且自动化敏感 sentinel 测试通过

#### Scenario: artifact 治理未确定
- **WHEN** artifact 的授权访问者或保留期尚未审批
- **THEN** CI MUST 跳过所有 artifact 上传，并记录 `artifact_status=skipped`；不得上传白名单摘要、受限原件或私有内容

### Requirement: 可信 Workspace 验收
[Phase 3] CI 能力 MUST 将 PR 离线验证、可信任务调度、目标 Workspace 运行结果和内容授权状态分别记录。目标 Workspace 真实任务和 artifact 隐私检查未完成时 MUST 保持相应验收项 pending，不得因 workflow 静态检查通过而宣称 Step 12 或全链路闭环完成。

#### Scenario: 仅离线 workflow 测试通过
- **WHEN** PR/CI 离线校验和 fake job 测试通过，但没有经过授权的 Workspace 运行证据
- **THEN** Step 12 平台验收 MUST 保持 pending
