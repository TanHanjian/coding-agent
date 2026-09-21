# Proposal: 面试 Agent 评测集

## Objective

[MVP] 为面试复盘 Agent 增加可重复的本地评测能力，覆盖工具调用行为和最终回答质量，并输出可审阅的分项分数。

## Non-goals

- [MVP] 不评测 HTTP/SSE 时序、取消和重连；这些继续由现有集成测试覆盖。
- [MVP] 不读取或修改用户 SQLite 数据，不实现长期记忆写入评测。
- [MVP] 不引入第三方评测平台。

## Delivery

[MVP] 使用版本化 JSONL fixture、原生 Go runner、确定性 scorer 和可选独立 LLM Judge。固定 Prompt 版本评测只接受指定版本或精确匹配缓存，失败时不回退本地 Prompt，报告基础设施失败且不具备发布资格；仅在线聊天允许回退并记录实际来源。评测保持本地基线，不引入第三方平台；后续平台接入仅为可选扩展。评测身份严格分离 case、run 和 execution，重传复用原 run/execution 标识，独立新评测才创建新的 run_id，repeat 使用独立执行身份。
