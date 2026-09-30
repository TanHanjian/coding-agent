# Tasks

## Phase 0: Contract Review

- [ ] Task: 固化 capability map 与跨模块术语
  - Acceptance: `entry-tree`、`agent-run`、`context-projection`、`context-compaction` 四个 module id、依赖方向和单 active leaf 边界写入并通过 review。
  - Verify: 逐条核对 proposal、design、四份 spec 的术语和状态一致；不得出现具名 Branch 或完整原始工具 payload 的隐含要求。
  - Files: `proposal.md`、`design.md`、`specs/*/spec.md`、`tasks.md`

- [ ] Task: 确认旧表双写、迁移失败和 source of truth 策略
  - Acceptance: 明确 `messages`、`generation_events`、`conversation_context_summaries` 在每个迁移阶段的读写职责和失败处理。
  - Verify: 形成迁移矩阵并通过设计 review。
  - Files: `design.md`、`tasks.md`

## Phase 1: Entry Tree Foundation

- [ ] Task: 定义 Entry、Message、ToolCall、ToolResult、Compaction 和安全投影领域契约
  - Acceptance: 类型具备稳定 ID、parent、payload version、role/kind 分离、toolCallId 配对和 schema version；未知 entry type 有明确兼容策略。
  - Verify: domain 单元测试覆盖 root/child、非法 parent、重复 ID、版本不兼容和工具配对。
  - Files: `backend/internal/domain/conversation/`、对应测试

- [ ] Task: 增加 Entry Tree repository port 与 SQLite migration
  - Acceptance: 可以读取 active leaf、按 expected parent 原子追加、回溯指定 leaf 路径和移动 active leaf；旧表数据不被删除。
  - Verify: SQLite 临时数据库测试并发 CAS、外键、索引、回滚和删除会话行为。
  - Files: `backend/internal/application/`、`backend/internal/infrastructure/repository/sqlite/`、`backend/internal/infrastructure/storage/migrations/`

- [ ] Task: 实现工具级安全投影和 artifact port
  - Acceptance: 未声明字段默认不存；敏感字段被拒绝；结果超过上限时只存预览和不透明 artifact 引用；artifact 按会话隔离并支持过期。
  - Verify: 字段白名单、凭据过滤、大小上限、路径穿越、跨会话读取、TTL、原子发布和清理测试。
  - Files: `backend/internal/domain/`、`backend/internal/application/`、`backend/internal/infrastructure/`

## Phase 2: AgentRun Lifecycle

- [ ] Task: 将当前 ChatTurn 生命周期映射为 AgentRun port
  - Acceptance: 一个 client id 只创建一个 Run；同一会话同时第二个请求返回 conflict；Run 状态覆盖 pending/running/waiting_tool/completed/failed/cancelled。
  - Verify: 幂等、并发开始、取消竞争、失败终态和新 client id 重提测试。
  - Files: `backend/internal/application/chat/`、`backend/internal/domain/`、SQLite repository 与测试

- [ ] Task: 迁移 generation event 与运行时 hub 的脱敏契约
  - Acceptance: RunEvent/Hub 保留 step 顺序、文本快照和安全工具摘要；原始参数、结果、凭据和内部错误不进入事件、日志或 SSE。
  - Verify: reconnect、慢订阅者、终态竞争、事件序列和隐私断言测试。
  - Files: `backend/internal/application/chat/generation.go`、`service.go`、HTTP stream 编码与测试

- [ ] Task: 实现重启前置收敛
  - Acceptance: 新实例在接受请求前将遗留 active Run/streaming read model 原子收敛为 `failed/generation_interrupted`；不恢复模型执行、不因 Hub 缺失单独判定失败。
  - Verify: 重启、单实例锁、恢复事务失败阻止就绪、重复恢复幂等和旧 client id 重试测试。
  - Files: 启动装配、storage/repository、chat recovery 与测试

## Phase 3: Context Projection

- [ ] Task: 从 active leaf 构造 provenance-preserving projection
  - Acceptance: 只读取当前 root-to-leaf 路径；branch 切换后旧路径不再进入上下文；compaction/context edit 规则有稳定顺序；每个投影消息可定位 source entry。
  - Verify: 树分支、上下文编辑、多个 compaction、空 root 和未知 metadata 测试。
  - Files: `backend/internal/agent/context/`、domain ports 与测试

- [ ] Task: 实现 provider-neutral AgentMessage 到 Eino schema.Message 转换
  - Acceptance: user/assistant/toolResult 的角色、tool call/result 关联和安全错误标记不丢失；不支持的结构返回明确错误；旧线性消息仍可兼容读取。
  - Verify: 普通文本、thinking/tool call、多个并发结果、旧历史、孤立/重复/缺失工具结果和 provider 边界测试。
  - Files: `backend/internal/agent/eino/executor.go`、context converter、测试

- [ ] Task: 将 ContextManager 从旧 History seam 迁移到 Entry/Projection seam
  - Acceptance: 调用方不能绕过 ContextManager 自行选择 history；`PreparedContext` 保留安全 metadata，不携带完整工具 payload；最终模型调用前可以取得预算校验结果。
  - Verify: manager 单测、executor 集成测试、旧 PassThrough 兼容测试。
  - Files: `backend/internal/application/chat/context.go`、`types.go`、`backend/internal/agent/context/`

## Phase 4: Context Compaction

- [ ] Task: 实现预算模型与完整逻辑组选择器
  - Acceptance: 预算满足“输入估算 + 输出预留 + 安全余量 <= 窗口”；用户轮次和 assistant tool-call/tool-result 组不可拆；无合法边界时明确失败。
  - Verify: 中文、代码、长参数、大结果、多工具、unknown window、超大单组和最终复检测试。
  - Files: `backend/internal/agent/context/`、预算/选择器测试

- [ ] Task: 实现 compaction 摘要生成、校验和树节点提交
  - Acceptance: 摘要只覆盖程序选定的完整前缀；摘要 schema、source boundary、预算和 tool protocol 校验通过后才追加 entry；失败不推进边界。
  - Verify: 摘要模型失败、空/超预算/非法摘要、CAS 冲突、重复提交和重启复用测试。
  - Files: `backend/internal/agent/context/`、summary repository、SQLite migration 与测试

- [ ] Task: 处理旧 `covered_sequence` 摘要迁移
  - Acceptance: 旧线性摘要不会被伪装成树节点；可通过明确的 legacy boundary 映射或失效重建；sequence 有间隙和旧数据不错误推进 tree boundary。
  - Verify: 已有 summary、无 summary、迁移中断、模型切换和重建测试。
  - Files: `backend/internal/agent/context/summary*`、migration、测试

## Phase 5: Compatibility and Cleanup

- [ ] Task: 完成 user/assistant 与安全工具 Entry 双写及回填
  - Acceptance: 新请求在事务边界内保持 Entry Tree、AgentRun、旧 messages/read model 的可诊断一致；双写失败不返回虚假成功。
  - Verify: 双写成功/失败、部分提交、回填重试、旧 API 读取和删除会话测试。
  - Files: chat turn store/service、repository、migrations、integration tests

- [ ] Task: 切换生产读取路径并保留兼容回退
  - Acceptance: Context Projection 成为新请求的模型历史来源；旧线性路径只作为受控兼容回退，且回退原因有 metadata。
  - Verify: 新树、旧历史、混合迁移会话、工具投影和压缩会话的端到端测试。
  - Files: chat service、context manager、Eino executor、tests

- [ ] Task: 完成文档、验收和代码 review
  - Acceptance: 四份 spec 的每项 MUST 都有测试或可观测验收证据；未实现功能不标记完成；不删除已有隐私和重启约束。
  - Verify: `go test ./...`、SQLite migration tests、HTTP/SSE integration tests、静态隐私扫描和人工 review。
  - Files: `docs/`、OpenSpec artifacts、测试报告
