# Tasks

## 1. 配置与完整计数

- [x] 1.1 增加显式窗口/输出/余量/模式配置与校验、.env.example，通过 config 测试验证缺失/非法/边界和无聊天路径。
- [x] 1.2 客户端发送一致输出上限，核对 eval/judge 兼容性，通过假 HTTP 测试验证两种输出字段不同时发送。
- [x] 1.3 增加完整请求计数和最终检查器，通过测试验证 schema/arguments/配对字段、边界、溢出、取消、非法/多模态输入和安全观测。

## 2. Graph 与摘要

- [x] 2.1 共享工具 schema 快照并在每次 Graph 模型入口检查，通过流式测试验证首轮拒绝、后续工具轮次拒绝、逐次通过和 errors.Is。
- [x] 2.2 同源装配真实模型身份/窗口/输出/余量，生成合法历史额度；摘要 invoker 检查最终 Prompt，通过装配与摘要测试验证无旁路及失败不推进 compaction。
- [x] 2.3 同步上下文工程和详细技术稿的 Graph-only 与已实现配置，核对文档不保留 ADK 前置和未经批准窗口默认。

## 3. 终态与用户反馈

- [x] 3.1 增加应用层稳定容量/计数分类，保持现有事务与终态优先级，通过 Run 测试验证可见文本、Entry head、取消和持久化失败回归。
- [x] 3.2 hub、snapshot、HTTP SSE 传播安全分类，通过订阅/重连/HTTP 测试验证固定文案且无原始错误泄露；更新 API 契约。
- [x] 3.3 补齐刷新历史的失败反馈，通过 TypeScript 检查及适用测试验证实时/历史一致且不把错误写为助手事实。

## 4. 集成验收

- [x] 4.1 执行 backend go test ./... 和适用竞态测试、前端 TypeScript 检查及 OpenSpec strict validate，记录真实结果。
- [x] 4.2 Review 实际请求边界、错误包装、配置一致性、Run 原子性与隐私，修复发现并同步验收记录。

## Verification record

- backend `go test ./...`、`go vet ./...` 通过；context/interview/chat/httpchat 核心包 10 次随机顺序回归通过，最后新增的 Prompt 增长、并发与工具组组合另做 3 次随机顺序回归通过。
- 前端 `npx tsc -b`、9 项 Node 测试通过，未启动前端服务。
- `npx @fission-ai/openspec@1.14.1 validate graph-model-budget-guard --strict` 通过。
- 规范及代码标准 review 的 P1 panic、P2 schema 大数精度、P2 工具双接口三项发现已修复，复核关闭。
- 当前 `CGO_ENABLED=0`，race 未通过：`go: -race requires cgo; enable cgo by setting CGO_ENABLED=1`。不以并发测试替代 race 结果。
- 部署窗口、输出、余量及端点模式须显式确认；JSON 字节估算非精确 tokenizer，生产 Usage 校准另行进行。

## Workflow follow-up

- 用户 review 后可按独立请求归档本变更；此任务不自动归档旧会话树变更。
