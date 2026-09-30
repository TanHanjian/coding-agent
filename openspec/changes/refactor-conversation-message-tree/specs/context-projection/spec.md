# Context Projection Specification

## Requirements

### Requirement: [MVP] Project only the active path

系统 MUST 从当前 active leaf 沿 parentId 回溯到 root，再按 root-to-leaf 顺序构造 Context Projection。非当前 active path 的分支 Entry MUST NOT 进入本次模型输入，除非以明确的 branch summary 形式被当前路径引用。

#### Scenario: Switch active branch

- **WHEN** active leaf 从旧路径移动到共同祖先并追加新的用户/助手消息
- **THEN** projection MUST 只包含共同祖先和新路径
- **AND THEN** 被放弃分支的原始消息不得自动混入模型输入

#### Scenario: Missing parent

- **WHEN** active leaf 路径存在缺失 parent 或跨会话 parent
- **THEN** projection MUST 失败并返回可诊断的一致性错误
- **AND THEN** 不得发送部分历史给模型

### Requirement: [MVP] Preserve source provenance

Projection MUST 为每个模型可见消息保留来源 Entry ID、投影版本和必要的边界 metadata。source provenance 只用于诊断、预算、回溯和后续编辑，不得把完整源 payload 自动写入日志或客户端。

#### Scenario: Locate projected message

- **WHEN** 诊断请求查询模型输入中的一条 projected message
- **THEN** 系统 MUST 能定位其 source Entry
- **AND THEN** 不能因为诊断而返回未授权的工具原文、凭据或隐藏内容

### Requirement: [MVP] Convert provider-neutral messages without silent loss

系统 MUST 将 provider-neutral system/user/assistant/toolResult 消息转换为当前模型适配器可接受的消息。转换 MUST 保留角色、tool call ID、tool name、结果错误标记和消息顺序；遇到不支持的结构 MUST 返回明确错误或经过规范批准的安全降级，不得静默丢字段。

#### Scenario: Assistant tool call and results

- **WHEN** projection 包含一个 assistant tool call 和其多个 tool result
- **THEN** 转换结果 MUST 保持合法 provider 顺序和每个 call ID 的关联
- **AND THEN** 下一次模型调用能收到对应的工具结果

#### Scenario: Unsupported media or block

- **WHEN** 当前适配器不能表达某个 provider-neutral block
- **THEN** 转换 MUST 返回分类错误或使用已批准的有界替代
- **AND THEN** 不得发送一个缺少关键上下文的貌似成功请求

### Requirement: [MVP] Validate tool protocol before model call

每次模型调用前，系统 MUST 检查 assistant tool call 与 toolResult 的数量、ID、顺序和错误标记关系；不得发送重复、孤立、缺失响应或仍处于未完成执行状态的工具交互。

#### Scenario: Incomplete tool group

- **WHEN** 当前 projection 中存在未完成 tool call 或缺失 result
- **THEN** 系统 MUST 等待合法终态、追加明确的中止结果或返回上下文协议错误
- **AND THEN** 不得把悬空工具交互发送给模型

### Requirement: [MVP] Keep legacy read compatibility during migration

迁移期间系统 MUST 能将旧的 user/assistant `conversation.Message` 作为有限 provider-neutral text message 读取；旧消息没有 tool call、tool result、thinking 或 branch provenance 时 MUST 标记为 legacy projection，不得伪造缺失结构。

#### Scenario: Project legacy history

- **WHEN** 会话尚未回填 Entry Tree 但需要继续聊天
- **THEN** 系统 MUST 在预算内以 legacy text projection 提供历史
- **AND THEN** 新请求的 Entry Tree 写入/回填状态必须可诊断
