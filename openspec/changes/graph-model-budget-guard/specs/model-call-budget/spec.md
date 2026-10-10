# Spec Delta

## Purpose

为本地面试记忆 Agent 的模型调用提供明确的容量边界，避免首次历史准备后工具循环增长绕过预算。用户能够区分容量中断与普通模型失败，已保存的会话事实和部分回答在失败后仍保持一致。

## ADDED Requirements

### Requirement: Explicit model capacity

[Phase 2] 系统 MUST 为启用的聊天模型显式确定上下文窗口、正输出上限、非负安全余量和端点支持的输出限制模式。输出上限与余量之和 MUST 小于窗口；未知或非法配置 MUST 阻止未经防护的聊天运行时创建。

#### Scenario: Capacity missing or invalid

- **WHEN** 用户配置模型但缺少窗口或容量组合非法
- **THEN** 系统 MUST 返回只包含配置字段的错误，不静默使用通用窗口

#### Scenario: Offline records without chat

- **WHEN** 用户未配置聊天模型
- **THEN** 普通离线记录 MUST 保持可用

### Requirement: Count complete model input

[Phase 2] 输入估算 MUST 覆盖最终渲染消息、摘要、材料、每个工具调用及其参数、结果配对字段、完整工具参数 schema 和协议开销。估算 MUST 明确精度与版本，不得计入未发送的本地观测元数据。

#### Scenario: Tool schema or arguments grows

- **WHEN** 只有工具参数 schema 或调用 arguments 增长
- **THEN** 输入估算 MUST 随之增长，不能只计算消息正文

#### Scenario: Unsupported or uncountable input

- **WHEN** 输入形态不受支持、消息非法或工具 schema 无法转换
- **THEN** 系统 MUST 受控拒绝，不调用模型、不 panic、不按零 token 放行

### Requirement: Guard every actual model call

[Phase 2] 每次聊天与其摘要模型请求 MUST 在发送前校验输入估算加输出上限加安全余量不超过窗口，且请求实际输出限制 MUST 与预留一致。超限 MUST 停止该请求，不能缓存首轮结果或自动原样重试。

#### Scenario: Exact boundary

- **WHEN** 估算与预留总和等于窗口
- **THEN** 系统 MUST 允许调用；超过一个单位时 MUST 拒绝且模型调用次数为零

#### Scenario: Tool loop grows

- **WHEN** 首轮通过但工具返回导致下轮超限
- **THEN** 系统 MUST 在下一次模型请求前拒绝，并保留已成功提交的闭合工具事实

#### Scenario: Summary request exceeds capacity

- **WHEN** 最终摘要 Prompt 超限
- **THEN** 系统 MUST 不调用摘要模型、不推进覆盖边界；任何降级聊天仍 MUST 通过最终检查

### Requirement: Atomic budget failure finalization

[Phase 2] 预算失败 MUST 使用稳定错误分类和现有原子终态路径，保留取消及持久化错误优先级、已保存可见文本和已提交事实。失败 MUST 不提交最终回答事实；开放工具组的活动边界 MUST 回到最近闭合位置。

#### Scenario: Partial answer then failure

- **WHEN** 容量拒绝前已有保存文本
- **THEN** 已保存文本 MUST 保留，Run 与 Message MUST 同步失败且无最终回答事实

#### Scenario: Terminal transaction fails

- **WHEN** 终态存储失败
- **THEN** 系统 MUST 不广播尚未落库的终态

### Requirement: Consistent safe failure feedback

[Phase 2] 实时订阅、终态重连和刷新历史 MUST 呈现一致的容量或计数失败原因。用户反馈、日志和指标 MUST 不包含原始模型错误、Prompt、工具参数/结果正文、凭据或主机路径。

#### Scenario: Reconnect after budget failure

- **WHEN** 用户重新订阅已失败回答或刷新历史
- **THEN** 系统 MUST 保持与实时相同的安全失败分类，而不是只建议原样重新生成

#### Scenario: Ordinary provider failure

- **WHEN** 失败不属于容量或计数分类
- **THEN** 用户 MUST 继续收到通用失败反馈而不暴露 provider 原始错误
