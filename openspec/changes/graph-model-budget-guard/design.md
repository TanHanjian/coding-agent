# Design

## Context

动机见 proposal.md，详细审查和 A1–A16 验收矩阵见 `docs/graph-model-budget-technical-design.md`。用户已批准全部实施切片，继续使用 Eino Graph。

## Goals / Non-Goals

Goals：实际请求边界完整计数；配置、客户端输出 cap 与最终检查同源；复用既有 Run/Entry 终态契约。

Non-Goals：不新增压缩或 artifact，不迁移 ADK，不扩大为多模型路由，不宣称任意 provider 的精确 token 保证。

## Decisions

1. 模型容量采用显式进程配置；不按模型名称猜测兼容端点窗口。字段为 OPENAI_CONTEXT_WINDOW_TOKENS、OPENAI_MAX_OUTPUT_TOKENS、OPENAI_CONTEXT_SAFETY_MARGIN_TOKENS、OPENAI_OUTPUT_LIMIT_MODE。余量允许显式 0，但不能静默缺失；模式为 max_tokens/max_completion_tokens，不同时发送。生产数值由使用者依据端点填写，测试使用独立小窗口。
2. 保持历史准备 BudgetPolicy 与最终调用检查器的不同职责。共享模型窗口/输出/余量；分区额度按可用输入容量生成合法策略，避免小窗口被默认 32000 分区阻塞或预算漏扣余量。
3. 复用 TokenEstimator 接口，新增保守 UTF-8 字节 fallback，并按适配器发送字段构造可序列化请求表示。与 len/4 相比覆盖工具 schema、arguments、配对字段和结构开销。未知多模态拒绝；计数规则标记版本。它可能过早拒绝，后续用 Usage 校准。
4. Graph 现有 modelStateInput 累积消息后检查；工具 Info 读取一次，模型绑定与计数共享不可变 schema 快照。摘要 invoker 对构造完成的 Prompt 检查。相比另增 Graph 节点，这保留现有工具循环/Recorder 顺序。
5. 应用层定义可 errors.Is 识别的容量/计数失败契约，agent 返回包装错误。RunService 保留取消和持久化失败优先，复用 FinishAssistant；generation snapshot/update 传播安全 code，HTTP 和历史前端使用固定映射。避免应用层反向导入 agent。
6. 无聊天配置保持离线启动；已配置聊天却缺预算时拒绝。通用模型工厂/独立 eval judge 不强行继承聊天预算，但配置输出 cap 时须合法，server 必须验证完整预算。

## Risks / Trade-offs

- [保守计数提前拒绝中文/JSON] → 文档说明估算限制；保留 tokenizer 替换接口和数值观测。
- [新增必填配置影响已启用聊天的启动] → .env.example 明确字段，无 schema 迁移；不能通过撤掉 guard 的开关回滚。
- [错误被 Graph/stream 包装丢失] → 流式 Graph 测试验证 errors.Is，测试下一轮模型调用次数。
- [Run 已有部分事实/文本] → 完全复用现有 finalizer/事务；回归开放/闭合工具组及终态优先级。
- [端点输出字段兼容不同] → 显式模式与本地假 HTTP 请求序列化测试。

## Migration Plan

先配置/计数，再 Graph/摘要，随后安全反馈；测试覆盖 A1–A16。部署前依据实际模型填写四个预算变量。回滚整体代码与配置到前版本，不提供生产跳过检查入口；数据格式不变。旧 context-engineering 文档同步 Graph-only 决定。当前生产模型的真实数值属于部署配置，不由实现猜测。
