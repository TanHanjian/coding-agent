# 代码实现总计划

## 目标

按“React Web 前端 + Go AI Agent 后端 + Eino + SQLite”的本机单用户架构，分步骤完成 MVP、Phase 2 和 Phase 3。每一步都必须先完成对应的 SDD 文档评审，再进入代码实现。

## 交付顺序

| 步骤 | 里程碑 | 主要交付 | 依赖 |
| --- | --- | --- | --- |
| 0 | 工程基线 | Go 服务、React 工程、配置、健康检查、测试入口 | 无 |
| 1 | 数据基础设施 | SQLite、迁移、事务、数据目录、日志与错误协议 | 0 |
| 2 | 题库 MVP | 题目、模板、作答、复盘、附件、搜索 API | 1 |
| 3 | 模型配置 | 模型档案、聊天/嵌入端点、凭据抽象、连接测试 | 1 |
| 4 | Eino Agent | Agent runtime、工具注册、检索工具、确认式记忆写入 | 2、3 |
| 5 | RAG 索引 | 全文/向量索引、增量更新、重建任务、引用模型 | 2、3、4 |
| 6 | AI 流式 API | Vercel AI SDK 兼容流式接口、取消、会话持久化 | 4、5 |
| 7 | React Web | Codex 风格工作区、六个主入口、题库和聊天界面 | 2、6 |
| 8 | Phase 2 | 分级提示、作答诊断、用户确认保存 | 4、6、7 |
| 9 | Phase 3 与加固 | FSRS、备份恢复、导出、性能和发布验证 | 2、5、7 |

## 每一步的 SDD 产物

每个步骤创建一个独立 OpenSpec change，固定包含：

1. `proposal.md`：目标、范围、非目标和阶段归属。
2. `specs/`：可观察需求、失败场景和验收条件。
3. `design.md`：模块、接口、数据流、状态和错误处理。
4. `tasks.md`：按实现顺序拆分的可执行任务。

完成代码后执行测试和验收，最后归档该 change，再开始下一步。

## 统一技术约定

- React/TypeScript 只负责 Web 界面和 API 客户端，不直接访问 SQLite 或模型端点。
- Go 是业务 API、数据访问和 AI Agent 编排的唯一后端入口。
- 普通能力使用 REST/JSON，并使用 chi 组织 Go 路由；AI 对话使用 Vercel AI SDK 可消费的流式协议。
- Eino 负责 Agent、工具调用和上下文编排；业务规则通过 Go service/tool 接口提供。
- SQLite 只由 Go 访问，所有写入经过事务和迁移机制。
- Go 服务默认只监听本机地址；凭据通过后端封装的安全存储管理。
- 普通题库操作离线可用；模型请求仍须由用户显式触发。后续可选平台 Trace 另按明确授权发送，不引入默认遥测或必接平台。
- 完整 Trace 默认关闭，与脱敏 Trace 独立配置；配置不能代替授权。授权、撤销及数据边界见 `specs/local-data-and-settings/spec.md`。
- 生成中重启按 Step 4 约定：独占本机单进程数据目录，在接受请求前原子收敛遗留 `streaming` 为 `failed` / `generation_interrupted`，保留文本，不恢复 checkpoint。
- 评测保持本地 MVP 基线；固定版本解析、发布资格及 case/run/execution 身份遵循 [评测设计](../add-agent-evaluation/design.md)，后续平台仅为可选扩展，不改变各里程碑范围。

## 统一完成标准

- 对应 SDD 中的每个 MUST 场景都有自动化测试或可重复验收步骤。
- API 契约、数据库迁移和错误码有版本记录。
- 失败、取消、重试和服务不可用状态均有明确行为。
- 普通日志、诊断信息和 UI 工具摘要不得包含完整题目正文、个人答案或原始工具 payload；它们与授权 Trace 是不同通道。完整本地备份允许包含题目、作答等业务数据和托管图片，但不得包含密钥；任何 Trace 也永不上传凭据，原始错误必须脱敏。完整真实学习内容仅可在另行明确授权的完整 Trace 范围内上传。
- 前后端可独立启动，也可使用统一开发命令联调。

## 会话管理预留

后续增加简单用户会话时，在 Go API 层增加 session middleware 和 session service。会话凭据通过 HttpOnly、SameSite Cookie 传递，业务 handler 只读取 context 中的当前用户；会话过期、注销和存储方式在独立 SDD change 中确定，不在 Step 1 实现。
