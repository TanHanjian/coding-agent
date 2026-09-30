# Context Compaction Specification

## Requirements

### Requirement: [Phase 2] Enforce final model budget

每次实际模型调用前，系统 MUST 校验输入估算、输出预留和安全余量的总和不超过模型上下文窗口。输入估算 MUST 包括 system 指令、工具 schema、会话摘要、当前请求、近期历史、工具参数和工具结果的安全投影。

#### Scenario: Tool loop grows after initial preparation

- **WHEN** 初始请求在预算内，但后续 tool call/result 使下一次模型输入超出预算
- **THEN** 系统 MUST 在下一次调用前重新选择安全缩减或压缩
- **AND THEN** 不得沿用首次检查结果直接发起超限调用

#### Scenario: Unknown context window

- **WHEN** 模型上下文窗口或输出上限未知
- **THEN** 系统 MUST 拒绝静默采用不受验证的默认值
- **AND THEN** 必须返回明确配置错误或使用已批准的模型配置

### Requirement: [Phase 2] Select legal compaction boundaries

压缩选择器 MUST 以完整用户轮次和完整 assistant tool-call/toolResult 逻辑组为边界，由程序决定可移除前缀；LLM 只能总结已经选定的消息。当前用户请求和未完成工具组 MUST 属于保护区。

#### Scenario: Boundary falls inside tool group

- **WHEN** token 预算计算出的边界落在 assistant tool call 与对应 result 之间
- **THEN** 系统 MUST 将边界移动到该完整逻辑组之前或之后的合法位置
- **AND THEN** 不得只保留 tool call 或只保留 tool result

#### Scenario: Oversized indivisible group

- **WHEN** 一个不可拆分的完整组本身超过可用预算
- **THEN** 系统 MUST 先尝试工具安全投影/artifact 或明确容量错误
- **AND THEN** 不得通过删除用户要求或制造悬空工具消息解决

### Requirement: [Phase 2] Generate and persist compaction atomically

系统 MUST 使用已选定且快照一致的前缀生成结构化摘要，并在摘要 schema、预算、工具协议和 active leaf/version CAS 全部成功后追加 compaction Entry。摘要失败、空摘要、超预算摘要、存储失败或 CAS 冲突 MUST 不推进 compaction 边界。

#### Scenario: Successful compaction

- **WHEN** 当前 active path 的完整前缀通过选择、摘要和校验
- **THEN** 系统追加包含 summary、summary version、firstKeptEntryId 和 tokensBefore 的 compaction Entry
- **AND THEN** 下次 projection 能用摘要替代被覆盖前缀并保留近期合法后缀

#### Scenario: Concurrent append during summarization

- **WHEN** 摘要模型处理期间同一会话 active leaf 已被其他 Run 推进
- **THEN** CAS MUST 失败并丢弃候选提交
- **AND THEN** 系统必须重新读取最新 projection，不得覆盖新消息

### Requirement: [Phase 2] Reuse persisted compaction safely

系统 MUST 在重启或后续 Run 中复用当前 active path 上合法的最新 compaction；复用前 MUST 校验 summary schema、firstKeptEntryId、source path 和当前预算配置。无效或过期摘要 MUST 失效并在预算允许时重建，不能静默当作完整历史。

#### Scenario: Reuse after restart

- **WHEN** 服务重启且 compaction entry 与 active path 完整
- **THEN** projection MUST 生成与重启前语义一致的摘要加保留后缀
- **AND THEN** 不得重新总结已经覆盖的原始前缀

#### Scenario: Invalid boundary

- **WHEN** firstKeptEntryId 不在 compaction parent 的祖先路径或 source version 不匹配
- **THEN** 系统 MUST 拒绝该 compaction projection 并进入明确的重建/容量错误路径
- **AND THEN** 不得把不相关分支内容拼进上下文

### Requirement: [Phase 2] Persist summary coverage by tree boundary

新 compaction MUST 使用 Entry ID/path snapshot 表示覆盖范围，不得把旧线性 `covered_sequence` 或运行时数组下标当作树覆盖水位。旧摘要只有经过明确 legacy mapping 或失效重建后才能参与新 projection。

#### Scenario: Legacy summary with sequence gaps

- **WHEN** 旧摘要存在 sequence 间隙或对应历史尚未回填 Entry Tree
- **THEN** 系统 MUST 标记其为 legacy summary 并阻止错误推进 tree boundary
- **AND THEN** 可以在安全预算内重建摘要或返回信息不完整/容量错误
