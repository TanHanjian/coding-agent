# Spec Delta

## Purpose

为本地面试 Agent 建立可审计、可版本化的 CozeLoop Code 与 LLM 评估器契约，并保留本地评分、授权边界及目标 Workspace 单独验收的要求。

## ADDED Requirements

### Requirement: 精确规则评估器
[Phase 2] 集成 MUST 提供独立的非空输出、必含内容、禁含内容和执行状态 Code 评估器；Tool Trace 结构评估器仅在启用时提供。各评估器 MUST 为每个适用样本独立返回分数和理由。任何本地确定性关键失败 MUST 继续由本地评测结果判定，不得由平台评估器覆盖。

#### Scenario: 有效样本通过或未通过规则检查
- **WHEN** 有效评测样本包含实际回答、执行状态及已声明的必含/禁含规则
- **THEN** 非空、必含、禁含和执行状态评估器分别返回分数和理由，并与对应本地精确断言具有相同的通过/未通过语义

#### Scenario: 空回答
- **WHEN** Agent 实际回答为空或仅包含空白字符
- **THEN** 本地确定性评分 MUST 记录 critical hard failure，且非空输出 Code 评估器 MUST 返回 0 分和非空理由

#### Scenario: 未声明 Tool Trace 规则
- **WHEN** 样本没有声明 Tool Trace 结构要求
- **THEN** 评估器不得把 Tool Trace 规则作为该样本的必需评分项

#### Scenario: 执行发生基础设施失败
- **WHEN** 样本因模型、Prompt 解析或执行环境问题被标记为基础设施失败
- **THEN** 该样本 MUST 标记为无效或未评分，不得把基础设施故障伪装成回答质量失败或通过

### Requirement: 独立 LLM 质量维度
[Phase 2] LLM 评估 MUST 将忠实度、指令遵循、完整性和可执行性作为四个可独立调试、版本化和查看的评分维度。每个维度 MUST 使用 0–4 分并提供非空理由；评估输入 MUST 明确映射问题、面试材料、实际回答和 case 中声明的参考事实，不得把不同维度的分数合并成无法追溯的单一分数。

#### Scenario: 完整输入可用
- **WHEN** CozeLoop 评估收到包含问题、面试材料、实际回答和参考事实的有效数据项
- **THEN** 四个维度分别返回 0–4 分及对应理由，且各自具有可追踪的评估器版本

#### Scenario: 必需评估输入缺失
- **WHEN** 某个样本缺少评估所需的实际回答或其他必需字段
- **THEN** 该样本 MUST 标记为无效或未评分，并明确指出缺失字段，不得静默当作 0 分

### Requirement: 评估器契约与版本审计
[Phase 2] 集成 MUST 为每个评估器保留稳定标识、精确版本、评估类型及字段映射；固定版本评测 MUST 使用明确版本，不得静默解析为其他版本。评估器的远程创建或查询只能使用已由官方公开 API/IDL 明确规定的操作；缺少明确契约时，集成 MUST 不得猜测 endpoint，并 MUST 提供可审阅的版本化定义及手动配置说明。

#### Scenario: 固定版本可追踪
- **WHEN** 评测运行绑定 Code 或 LLM 评估器
- **THEN** 本地记录包含评估器稳定标识和实际固定版本，以便复查同一评估配置

#### Scenario: 官方契约未定义评估器操作
- **WHEN** 官方公开 API/IDL 未定义所需的评估器创建或读取操作
- **THEN** 集成 MUST 不调用猜测的远程 endpoint，明确报告远程配置未自动化，并保留可供人工配置的版本化评估器定义

#### Scenario: 目标 Workspace 尚未实测
- **WHEN** 公开 API/IDL 和 fake server 测试已通过，但目标 Workspace 的权限、字段、版本或运行行为尚未实测
- **THEN** 验收状态 MUST 保持为 Workspace 待验证，不得报告为平台验收完成

### Requirement: 人工校准与发布边界
[Phase 2] LLM 评估器 MUST 支持对 10–15 个 case 记录人工分数、平台分数和差异说明。完成并审阅校准前，CozeLoop LLM 评分 MUST 仅作为 advisory 信息，不得成为 CI 或发布硬门禁；平台评分也不得改变本地评测的关键失败结论或发布资格。

#### Scenario: 记录人工校准样本
- **WHEN** 人工校准者审阅一个已由 CozeLoop LLM 评估器评分的 case
- **THEN** 校准记录分别保存人工分数、平台分数、差异和说明，并可关联 case 与评估器版本

#### Scenario: 校准尚未完成
- **WHEN** 可用校准样本少于 10 个或校准尚未获审阅通过
- **THEN** 平台 LLM 分数 MUST 标为 advisory，且不得作为 CI 或发布通过条件

### Requirement: 评测内容授权
[Phase 2] 上传用于评估器调试或执行的完整评测内容 MUST 同时满足功能配置开启和当前有效的内容上传授权；仅有配置开关不得视为授权。凭据 MUST NOT 被上传，错误或诊断输出 MUST 脱敏。

#### Scenario: 缺少有效授权
- **WHEN** 用户未授权、授权已撤销或授权绑定的 Workspace/服务范围不匹配
- **THEN** 集成 MUST 在发送完整评测内容前拒绝上传，并保留本地评测能力

#### Scenario: 授权有效
- **WHEN** 内容上传功能已开启且当前授权有效
- **THEN** 集成仅可上传当前评估所需的评测字段，且不得包含 API 凭据或未声明的本地数据
