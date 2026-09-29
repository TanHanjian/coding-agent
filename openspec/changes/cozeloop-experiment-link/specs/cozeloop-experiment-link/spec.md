# Spec Delta

## Purpose

定义如何基于已存在的本地评测运行和已同步的 CozeLoop 数据集版本创建实验、查询结果并按完整执行身份回关联，同时保持本地评测权威与内容授权边界。

## ADDED Requirements

### Requirement: 固定运行快照与身份核验
[Phase 2] 实验提交 MUST 绑定一个已完成的 live run、与该 run 同 Workspace/API scope 且仅包含该 Run 精确 item 集的 run-scoped CozeLoop 数据集不可变版本，以及每个 evaluator 的已发布不可变远端引用。共享 dataset ID 或 sync 成功状态 MUST NOT 被当作 run-scope 证明；若 Step 7–9 sync 未提供精确 run-scoped version，MUST 在任何实验写请求前 fail closed。report MUST 显式包含完整 `run_id + case_id + case_version + repeat_index`；实现 MUST 拒绝非 live report、缺失身份、推断补齐版本或重复身份。提交前 MUST 从 pinned version 回读 item ID 与完整 item 集，验证 Workspace/scope 一致、数据集 item 集与本地执行集合完全相等、每个 `item_id -> run_id + case_id + case_version + repeat_index` 唯一且完整，并验证本地 evaluator key 对应的远端 evaluator ID、immutable version ID 和内容 hash。MUST NOT 使用 latest、标签、草稿、远端未验证 hash 或仅按 case ID 推断关联。report 含任一基础设施失败样本时，MUST 在创建实验前拒绝整次提交。

#### Scenario: 快照与本地运行完全匹配
- **WHEN** 本地 live 报告、同 Workspace 的已同步数据集 pinned version 及远端 evaluator 不可变引用均可核验，且回读 item 集与完整执行身份完全匹配
- **THEN** 实验提交使用这些精确版本并保存可审计的唯一 `item_id` 身份映射

#### Scenario: 数据项多出、缺少或不可核验
- **WHEN** 数据集版本含额外/缺少/重复 item、身份或 Workspace scope 冲突、远端 evaluator 引用不匹配，或平台返回 `content_omitted` 且不能回读实际内容
- **THEN** 集成 MUST 拒绝提交，保留本地报告，并给出不含原始内容的安全失败分类；远端 `sync_content_hash` 单独存在不构成验证

### Requirement: evaluator 输入映射与脱敏结果
[Phase 2] Workspace PoC MUST 用互不相同的非敏感 sentinel 验证 Code evaluator 的 `actual_output` target-output 映射，以及每个 LLM evaluator 的声明输入字段，并核对真实 score/status 与固定脱敏原因码。远端 evaluator reason MUST 不得回显输入内容；本地 report、sidecar、日志及 CI 产物 MUST NOT 持久化 CozeLoop 返回的自由文本 reason，只允许保存 score/status 和通过 allowlist 校验的脱敏原因码。人工校准完成前，LLM 分数 MUST 标记为 advisory，不得用于质量门禁或发布结论。

#### Scenario: evaluator 字段映射未验证或 reason 含回显内容
- **WHEN** 任一 sentinel 到达错误字段、真实 score/status 无法核验、reason 回显输入或不是受控原因码
- **THEN** Workspace PoC MUST 失败；集成 MUST 不记录原始 reason，且不得进入实验实现/验收

#### Scenario: LLM evaluator 尚未人工校准
- **WHEN** 技术 PoC 与 item 身份关联通过，但 10–15 case 人工校准未完成
- **THEN** 传输/身份关联可继续验收，但 LLM 分数 MUST 仅作为 advisory

### Requirement: 显式提交与未知结果恢复
[Phase 2] 实验写操作 MUST 由操作者显式确认；提交前 MUST 验证目标 Workspace 支持所需实验模式、字段映射及固定版本绑定。远端写结果不确定时 MUST 记录 unknown 状态并停止自动重复写入，除非目标 Workspace 已验证的唯一查询键/幂等语义可确定原请求结果。MUST NOT 将本地指纹称作平台原生幂等键。

#### Scenario: 未显式确认
- **WHEN** 调用实验创建但操作者未提供明确应用确认
- **THEN** 命令 MUST 在创建平台客户端或发送远端写请求前拒绝操作

#### Scenario: 创建请求超时且无法确认是否成功
- **WHEN** 创建请求超时或连接中断，且无经 Workspace 验证的查询机制能判定远端结果
- **THEN** 集成 MUST 将该 run 标记为 unknown，不自动重发 POST，并允许通过已知实验 ID 或人工核对恢复

#### Scenario: Workspace 不支持无评测对象实验
- **WHEN** 目标 Workspace 实测显示实验必须绑定评测对象，或不能从评测集 `actual_output` 运行 evaluator
- **THEN** 集成 MUST 暂停实验提交，不得静默改为部署 Agent Endpoint 或伪造运行成功

### Requirement: 基础设施失败样本阻断
[Phase 2] 若 live report 中存在任何基础设施失败样本，实验命令 MUST 在任何实验写请求之前拒绝整次提交；MUST NOT 将该样本转成普通质量分数、从集合静默排除或仅降低有效分母。失败分类 MUST 不包含原始 case、回答或 evaluator reason。

#### Scenario: Run 包含基础设施失败
- **WHEN** report 的任一执行身份状态属于基础设施失败
- **THEN** 整个 Run 的实验提交 MUST 被阻断，不创建实验，并输出安全失败分类

### Requirement: 内容上传授权与本地结果隔离
[Phase 2] 每个实际携带完整 case、回答、Prompt、工具材料或其他受保护评测内容的远端请求 MUST 在发送前同时满足对应功能开关及匹配 Workspace/服务范围的有效内容授权。授权撤销后 MUST 阻止后续内容请求和 pending 重试。实验平台失败 MUST NOT 删除或改写本地评测质量结论。

#### Scenario: 无授权或授权已撤销
- **WHEN** 内容上传开关关闭、授权缺失/范围不符或授权已撤销
- **THEN** 集成 MUST 在发送内容前停止，保留本地报告并记录安全状态

#### Scenario: 平台实验失败
- **WHEN** 远端提交、轮询或评估结果处理失败
- **THEN** 本地报告、硬失败、无效样本数和发布资格 MUST 保持不变，平台状态单独记录

### Requirement: 分页结果完整性与身份回关联
[Phase 2] 实验结果 MUST 被完整分页读取并按经核验的 item 身份映射回本地执行；MUST 验证总数、重复项、缺失项、关联冲突及每个预期 evaluator 的结果完整性。MUST NOT 以数组顺序、case ID 单独匹配、空分数转零或缺失结果当作通过。

#### Scenario: 完整结果集可回关联
- **WHEN** 所有页完整读取，且每个预期 item/evaluator 结果均与已核验身份唯一对应
- **THEN** 实验状态可标记为完成，并附带精确版本引用和逐项结果关联

#### Scenario: 结果不完整或重复
- **WHEN** 分页结果缺失、重复、总数不符、身份冲突或 score/status 不完整
- **THEN** 实验 MUST 标记 incomplete 或 reconcile_failed，不得输出质量通过结论，并保留待核对项；自由文本 reason MUST 丢弃，不作为完整性判定依据

### Requirement: Workspace 验收分层
[Phase 2] 本地测试和 fake HTTP 测试 MUST 与目标 Workspace 验收分别记录。实验能力只有在目标 Workspace 的权限、固定版本、无评测对象运行（若适用）、字段映射、item ID 回关联、状态/分页与结果行为均有非敏感证据后，才可标为平台验收完成。

#### Scenario: Fake HTTP 已通过但 Workspace 未验证
- **WHEN** 本地领域测试和 fake HTTP 契约映射通过，但目标 Workspace 实测缺失，或实测未覆盖两个 Run 隔离、重复执行/部分写入重试、pinned version 精确 item 集与 distinct sentinel 字段映射
- **THEN** 验收状态 MUST 保持 Workspace pending，不得宣称 Step 10 或 CozeLoop 闭环完成
