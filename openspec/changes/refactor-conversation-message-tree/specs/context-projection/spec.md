# Context Projection Specification

## Scope

产品 MVP 的 Projection 在实施 Phase 3 实现；实施 Phase 1/2 MUST 不因新增 Entry/Run 持久化而提前切换模型 history。实施 Phase 2 保留旧线性历史和当前 Run 的内存工具循环；持久化安全投影不是原始工具协议。compaction 是产品 Phase 2、实施 Phase 4 的未来能力；artifact 为独立可选能力。参见 [design](../../design.md)、[Entry Tree spec](../entry-tree/spec.md)、[AgentRun spec](../agent-run/spec.md) 与 [Compaction spec](../context-compaction/spec.md)。

## Requirements

### Requirement: [MVP] Project only a consistent active path

实施 Phase 3 的系统 MUST 从当前 active leaf 沿 parent 回溯到 root，在一致读取快照中按 root-to-leaf 构造 Projection。非当前路径的事实 MUST NOT 自动进入模型输入。系统 MUST 验证会话归属、路径完整性和支持的版本，失败时 MUST 不发送部分历史。该内部树语义 MUST NOT 被解释为实施 Phase 2 已开放公共分支功能。

#### Scenario: Select a different closed path internally

- **WHEN** 在允许内部 head 操作的条件下，head 移到合法闭合祖先并形成新路径
- **THEN** Projection MUST 只包含共同祖先及新路径
- **AND THEN** 旧分支与失败的 pending 分支 MUST 不自动混入新请求输入

#### Scenario: Detect a corrupt or unsupported path

- **WHEN** active path 缺 parent、循环、跨会话、超容量或存在不支持的 payload version
- **THEN** Projection MUST 返回明确一致性/版本/容量错误，不静默跳过节点
- **AND THEN** 不得自动改 parent 或发送貌似完整的截断模型 history

### Requirement: [MVP] Preserve provenance and projection decisions

每条模型可见历史消息 MUST 保留 source Entry ID、payload/projection/policy version 和必要边界 metadata；安全工具事实 MUST 保留调用/结果来源关联、截断状态和拒绝/降级决策。legacy 文本 MUST 使用真实 legacy Message 来源，不伪造 Entry ID。provenance MUST NOT 自动暴露完整源 payload、隐藏内容或凭据到日志/客户端。

#### Scenario: Locate a downgraded tool fact

- **WHEN** 工具组以已批准的事实文本方式进入 Projection
- **THEN** 系统 MUST 能定位源 assistant/result Entry、call ID 和采用的安全映射策略
- **AND THEN** 文本 MUST 明示安全投影/截断含义，不被标注为原始参数或完整结果

#### Scenario: Diagnose a projected message safely

- **WHEN** 诊断查询某条 projected message 的来源
- **THEN** 系统 MUST 在会话授权边界内返回安全定位 metadata
- **AND THEN** 不得为诊断返回 raw tool、think、凭据、system prompt 原文或其他会话内容

### Requirement: [MVP] Never reconstruct original arguments from safe fields

模型适配器转换 MUST 区分完整的当前执行协议与持久化的历史安全投影。SafeToolCall 的白名单字段 MUST NOT 作为原始函数 arguments 拼装、再次执行或自动重放。无法合法表达时 MUST 明确拒绝，或采用经过批准、有界且保留 provenance 的历史事实文本降级；MUST NOT 承诺仅凭工具名、call ID 和安全字段重建原始协议。

#### Scenario: Reject an unsafe protocol reconstruction

- **WHEN** 历史 assistant/toolResult 只有投影字段、preview 或截断信息，而适配器要求原始参数
- **THEN** 系统 MUST 返回分类错误，不能生成貌似原始的 arguments JSON
- **AND THEN** 不得因历史调用而再次执行工具或从 artifact/旧事件补取未授权 raw payload

#### Scenario: Apply an approved factual-text downgrade

- **WHEN** 有明确批准的组级安全映射策略可以把闭合调用组表达为有界历史事实文本
- **THEN** 系统 MUST 一致映射整个组，保留调用/结果来源、错误分类与截断/降级说明
- **AND THEN** 该文本 MUST 不制造待执行 tool call 或宣称恢复了原始 tool protocol

### Requirement: [MVP] Convert supported messages without silent loss

系统 MUST 将支持的 user/assistant/toolResult 历史事实转换为当前适配器可接受的输入，保持角色语义、内容块/组次序、来源与安全错误标记。采用工具协议时 MUST 有信息完备且另行批准的合法映射，并保持 call/result ID 与工具名；采用事实文本时 MUST 标识批准降级。不支持的结构 MUST 返回明确错误或批准的安全替代，MUST NOT 静默丢字段或隐含要求特定 SDK。

#### Scenario: Convert supported text and safe tool facts

- **WHEN** Projection 包含文本与闭合安全工具组
- **THEN** 适配器 MUST 按批准策略保持模型可见事实及来源，不绕过安全投影
- **AND THEN** 缺原始参数时 MUST 走明确拒绝/事实文本路径，不承诺下一模型收到原始工具结果

#### Scenario: Encounter an unsupported block

- **WHEN** 当前版本或适配器不能表达某个 block
- **THEN** 转换 MUST 分类失败或使用批准的有界替代
- **AND THEN** 当前持久化契约 MUST 不因此要求 system/thinking/media 支持，也不得保存隐藏推理作为替代

### Requirement: [MVP] Validate tool groups before every model call

模型调用前 MUST 校验原始 Entry 路径中 assistant 声明与 toolResult 的身份、名称、数量、声明次序和错误标记关系，并校验转换后的模型消息。重复、孤立、缺失响应或 pending group MUST NOT 以工具协议发送，也 MUST NOT 通过事实文本降级掩盖未完成执行。

#### Scenario: Encounter an incomplete historical tool group

- **WHEN** 待投影路径存在未完成调用或缺失结果
- **THEN** 系统 MUST 返回上下文协议错误，不伪造 cancelled/aborted toolResult
- **AND THEN** 正常 Run 失败回退应使后续读取选择 LastClosedEntryID；若仍遇到 pending path，Projection MUST 不自行修改 head

#### Scenario: Continue the current in-memory tool loop

- **WHEN** 当前 Run 内工具仍在执行，或其结果事实 batch 未成功提交
- **THEN** 系统 MUST 等待合法完整结果和提交，才允许下一次模型调用
- **AND THEN** 不得用 checkpoint 文本或显示摘要补造配对；历史转换校验不能被降级绕过

### Requirement: [MVP] Explicit legacy compatibility and history cutover

实施 Phase 2 MUST 继续使用旧线性 History 与旧摘要；实施 Phase 3 才切换至 Projection。旧 user/assistant Message MUST 只作为标记来源的 legacy text 读取，缺工具/分支事实时 MUST 不伪造。混合迁移历史 MUST 有明确覆盖水位、去重和当前 user 处理规则；不完整/失败迁移 MUST 可诊断，MUST NOT 在 client id 重试中伪造 Run/Entry 回填。

#### Scenario: Continue a legacy conversation

- **WHEN** 旧历史尚未映射 Entry，但会话需要继续聊天
- **THEN** 系统 MUST 通过显式 legacy text 来源在安全预算内兼容读取，不无提示丢掉旧历史
- **AND THEN** legacy Message 与新 Entry 的边界 MUST 可诊断，缺失工具协议 MUST 不被补造

#### Scenario: Combine mapped and unmapped history

- **WHEN** 一段历史已显式迁移而另一段仍是 legacy text
- **THEN** Projection MUST 按明确水位选择来源，避免重复历史或重复本次 user
- **AND THEN** 不一致边界 MUST 返回明确错误或受控兼容结果，不静默绕过协议/隐私校验

### Requirement: [Phase 2] Project validated compaction within source boundaries

在后续 compaction 启用后，Projection MUST 仅使用当前 active path 上验证通过的 compaction，以摘要替代明确覆盖的前缀并保留 firstKeptEntryId 后的合法后缀。摘要 MUST 继承来源/安全边界，不含其他分支、raw 工具、内部推理或凭据；旧 covered_sequence MUST 先经显式 legacy mapping 或失效重建，不能当树水位。

#### Scenario: Reuse a valid persisted compaction

- **WHEN** compaction 的版本、source path、firstKeptEntryId 与预算配置均合法
- **THEN** Projection MUST 输出来源可追踪的摘要加保留后缀，不重复被覆盖前缀
- **AND THEN** 工具组 MUST 保持闭合，摘要不得引入新的工具执行要求

#### Scenario: Reject an unrelated or invalid summary

- **WHEN** compaction 边界不属于当前祖先路径、版本无效，或旧 sequence 映射不明确
- **THEN** Projection MUST 拒绝该摘要并走明确重建/容量错误路径
- **AND THEN** 不得从不相关分支拼接上下文，也不得把未启用 artifact 当作必需读回来源
