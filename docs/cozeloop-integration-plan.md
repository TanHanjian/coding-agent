# CozeLoop 调用追踪集成方案

- 状态：Proposal
- 目标：为 Eino Agent 增加可选的 CozeLoop 调用追踪、Token 用量、延迟和错误观测能力。
- 适用范围：本地开发、评测命令和后续受控部署环境。
- 核心原则：CozeLoop 是可观测性适配器，不参与业务决策，不替代本地评测，不替代 ContextManager，也不影响主链路可用性。

## 文档边界与上下文工程关联

本文只定义 CozeLoop 观测集成，不是上下文压缩的实施规范。长对话、工具结果缩减、会话摘要持久化和未来长期记忆边界见 [上下文工程设计与实施计划](context-engineering-design.md)（Draft，待 review）。

当前 ContextManager 默认仍全量透传，预算与摘要模块尚未完整实现；已有摘要表结构不代表摘要功能已生效。当前 Executor 仍使用 Graph Runtime，不能把 ADK middleware 直接注册为 Graph callback。后续运行时迁移需要独立验证；本文的 Proposal 和步骤也不代表相关能力均已交付。

## 1. 为什么接入 CozeLoop

当前 Agent 已经具备：

- Eino Graph；
- ChatModel 调用；
- Tool Calling 循环；
- Graph 节点调试日志；
- 本地评测集和 Judge；
- Step、Tool Input、Tool Output 等运行事件。

现有日志适合查看本地元数据，但不适合系统分析：

- 一次 Run 的完整调用链；
- 每个 ChatModel Step 的耗时；
- Tool 调用前后关系；
- Prompt/Completion/Total Token；
- 模型错误和超时；
- 不同评测 case 的耗时与成功率。

CozeLoop 的 Eino Callback 可以在不改 Agent Graph 业务代码的情况下补齐这些观测能力。官方 Eino 扩展提供 `callbacks/cozeloop`，典型接入方式是创建 CozeLoop client，然后注册 `NewLoopHandler(client)`。

## 2. 边界

```text
HTTP / Eval CLI
      |
      v
应用装配层
  |-- Agent Executor
  |-- CozeLoop Callback（可选）
      |
      v
Eino Graph
  |-- prepare_context
  |-- chat_template
  |-- chat_model
  |-- tools
      |
      v
CozeLoop
```

CozeLoop 只观察 Eino Callback，不承担：

- 上下文裁剪；
- Token 预算决策；
- 会话摘要；
- 题库查询；
- 评测打分；
- 聊天消息持久化；
- 错误恢复和重试决策。

## 3. 依赖

新增两个 Go 依赖：

```text
github.com/cloudwego/eino-ext/callbacks/cozeloop
github.com/coze-dev/cozeloop-go
```

版本应在实施时锁定为与当前 `github.com/cloudwego/eino v0.9.19` 兼容的稳定版本，并通过 `go mod tidy` 固化到 `go.mod` 和 `go.sum`。不直接依赖 CozeLoop 内部包。

## 4. 配置设计

扩展 `config.Config`：

```go
type CozeLoopConfig struct {
    Enabled       bool
    WorkspaceID   string
    APIToken      string
    TraceEnabled   bool
    CaptureContent bool
    Environment   string
    ServiceName   string
}
```

推荐环境变量：

```text
COZELOOP_ENABLED=false
COZELOOP_WORKSPACE_ID=
COZELOOP_API_TOKEN=
COZELOOP_TRACE_ENABLED=false
COZELOOP_CAPTURE_CONTENT=false
COZELOOP_ENVIRONMENT=local
COZELOOP_SERVICE_NAME=interview-memory-agent
```

配置规则：

- `COZELOOP_ENABLED=false` 时不创建 client，不注册 Callback；
- `COZELOOP_ENABLED=true` 时必须校验 Workspace ID 和 API Token；
- 配置错误应在应用启动阶段明确失败，不在第一次聊天请求时才失败；
- Token 只从环境变量或 `.env` 读取，不进入 SQLite、HTTP 请求或普通日志；
- Trace 通过独立的 `COZELOOP_TRACE_ENABLED` 配置，默认关闭；启用后默认只上传脱敏元数据；
- `CaptureContent` 默认关闭，开关不是用户授权；完整内容还必须通过第 9 节的授权检查；
- 无论模式如何，凭据永不作为观测内容上传，错误始终脱敏。

## 5. 装配模块

新增基础设施模块：

```text
backend/internal/infrastructure/observability/cozeloop/
  client.go
  client_test.go
```

模块接口建议：

```go
type Provider interface {
    Handler() callbacks.Handler
    Close(context.Context) error
}

func New(ctx context.Context, cfg config.CozeLoopConfig) (Provider, error)
```

实现职责：

1. 校验启用状态和配置；
2. 调用 `cozeloop.NewClient()`；
3. 仅在 `TraceEnabled=true` 时创建 `cozeloopcallback.NewLoopHandler(client, ...)`；
4. 仅在独立 Trace 开关开启时注册安全 Parser；逐次上报检查 `CaptureContent` 和有效授权，不直接使用可能泄露内容的默认 Parser；
5. 返回 Handler 和关闭函数；
6. 不创建 Eino ChatModel，不持有业务 Service，不读取 SQLite。

## 6. Callback 注册策略

### 6.1 推荐：进程级注册一次

以下仅展示官方扩展的注册机制；实际装配必须先检查独立 Trace 开关，使用安全 Parser 和逐次授权检查，不能无条件注册默认 Handler：

```go
client, err := cozeloop.NewClient()
handler := cozeloopcallback.NewLoopHandler(client)
callbacks.AppendGlobalHandlers(handler)
```

当前应用是单进程服务，因此在 `cmd/server/main.go` 的应用装配阶段初始化一次即可。

推荐顺序：

```text
config.Load
  -> storage.Open / Migrate
  -> CozeLoop 初始化
  -> ChatModel 初始化
  -> Agent Executor 初始化
  -> HTTP Server 启动
```

### 6.2 避免重复注册

`callbacks.AppendGlobalHandlers` 是进程级行为，不能在每次请求、每次 `Executor.Stream` 或每个评测 case 中调用。

必须保证：

- server 启动只注册一次；
- eval CLI 进程启动只注册一次；
- 单元测试默认不启用；
- 初始化失败时不留下半初始化状态；
- client 在进程退出时关闭并 flush。

### 6.3 为什么不直接塞进 Executor

Executor 目前已经通过 `compose.WithCallbacks` 注入本地 Graph 事件。CozeLoop 属于横切的观测能力，放到 Executor 内会导致：

- 每个 Executor 都管理外部 client；
- 测试需要额外配置；
- server 和 eval 的生命周期不一致；
- 未来其他 Eino Graph 无法复用。

因此推荐在应用装配层注册全局 Handler，同时保留现有本地 callback。

## 7. Trace 结构

CozeLoop Trace 应按以下层级观察：

```text
Agent Run
  +-- prepare_context
  +-- chat_template
  +-- chat_model Step 1
  |     +-- tool call: search_question_memory
  |     +-- tool result
  +-- chat_model Step 2
  +-- tools
  +-- final output
```

建议统一命名：

| 对象 | 建议名称 |
|---|---|
| 根 Agent | `interview_review` |
| Graph | `interview_review_graph` |
| 上下文 | `prepare_context` |
| Prompt | `chat_template` |
| 模型 | `chat_model` |
| 工具节点 | `tools` |
| 摘要模型 | `context_summarizer` |

现有 `agentName = "interview_review"` 应作为根 Trace 的稳定名称，不要使用会变化的会话标题或用户问题作为名称。

## 8. Trace 元数据

默认记录非敏感元数据：

```text
service.name
service.environment
agent.name
conversation.id
assistant.message.id
model.name
run.mode
case.id                  # 仅 eval
case.tags                # 仅 eval
step.id
tool.name
status
error.kind
duration_ms
input_tokens
output_tokens
total_tokens
```

字段规则：

- ID 可以记录，便于本地日志和 CozeLoop Trace 互相定位；
- 不把 API Key、Authorization、完整数据库路径放入 baggage 或 tags；
- `conversation.id` 和 `assistant.message.id` 只用于定位，不应被当成用户身份；
- eval 的 `run.id`、`case.id`、`case.version`、tag 和 `repeat.index` 应进入 Trace 元数据；case identity 为 `case_id + case_version`，结果身份为 `run_id + case_id + case_version + repeat_index`；每次新运行生成新 `run_id`，同一结果重传复用原身份，commit/model/prompt 仅作元数据；
- 不把用户问题拼进 Trace 名称，避免名称不可聚合。

## 9. 输入输出内容策略

### 所有环境的默认值

开发、本地评测、CI 和真实用户环境均默认不上传完整 Trace；脱敏 Trace 由独立的
`COZELOOP_TRACE_ENABLED` 开关启用，只保留节点/Tool 名、Token、延迟、错误分类和脱敏 ID，不包含内容摘要。

### 完整内容授权

用户明确授权后，允许上传真实完整学习内容，不限于 fixture；内容可包括授权范围内的
Prompt、模型输出、Tool 参数与结果、上下文和摘要输入输出。必须同时启用 Trace、
`COZELOOP_CAPTURE_CONTENT=true` 并通过有效授权校验；配置开关本身不构成授权。

授权须明确目的、接收平台（含工作空间）及内容范围；本地记录授权时间、范围和告知版本。
接收平台或内容范围变化须重新授权。用户撤销后停止后续完整上报（包括尚未发送的队列内容）；
已上传内容不会自动删除，须在授权告知中说明。无授权或超出范围时只允许独立启用的脱敏 Trace。

凭据（API Key、Authorization、Cookie 等）永不作为内容上传；错误在所有模式下始终脱敏。
本规则同时适用于 SDK Prompt Trace、Candidate/Judge Trace 和评测内容同步，不能由其他出口绕过。
评测报告继续写本地 JSON/Markdown，CozeLoop 只是额外的 Trace。

不要依赖 CozeLoop 作为唯一业务审计记录。

## 10. 自定义 Data Parser

Eino CozeLoop Callback 支持自定义 `CallbackDataParser`。建议提供一个项目内 Parser 适配器，而不是完全依赖默认 Parser：

```go
type DataParser struct {
    CaptureContent bool
}
```

Parser 负责：

- 仅在 `CaptureContent=true` 且有效授权覆盖当前内容时保留输入输出；撤销后停止后续完整上报；
- 其余情况只保留允许的脱敏元数据、长度、Token 和错误分类，不保留内容摘要；
- 对 Tool 参数和结果按配置截断；
- 删除 API Key、Authorization、Cookie、文件路径等字段；
- 不记录内部错误堆栈中的请求头和 URL 查询参数。

Parser 不负责：

- 改变 Eino Graph 行为；
- 修改模型消息；
- 重新计算 Token；
- 写本地数据库。

## 11. Token 观测

CozeLoop/Eino Callback 可以读取 Eino ChatModel 的 `ResponseMeta.Usage`，包括：

- Prompt Tokens；
- Completion Tokens；
- Total Tokens；
- 部分模型的 Cached/Reasoning Token。

注意：这类 Usage 是**模型调用完成后的实际用量**，不能替代 ContextManager 的调用前预算。

两者分工：

```text
ContextManager + TokenEstimator
    -> 跨轮上下文准备、初始预算与会话摘要

运行层上下文策略 + 最终输入预算检查
    -> 每次模型调用前控制工具循环增长；包含所有临时注入与工具 schema

CozeLoop Callback + ResponseMeta.Usage
    -> 调用后实际统计和追踪
```

在 Tool Loop 中，每次 `chat_model` Step 都有独立 Usage，需要由 CozeLoop 按 Step 记录，并在 Agent Run 层聚合。

当前已有 `graph_events.go` 处理流式输出。实施时不要在 Executor 主循环中手工从文本 chunk 计算 Token；优先让 CozeLoop Callback 消费 Eino 的 CallbackOutput。若流式 Usage 只出现在最后一个没有文本的 chunk，Parser 必须保留该 chunk 的 Usage，不能因为 Message 为空而丢弃。

### 11.1 上下文工程观测契约（待实现）

除 Usage 外，补充以下本地指标，并可通过安全 Parser 映射到 CozeLoop；字段名与平台映射需实施验证：

| 指标 | 含义 |
| --- | --- |
| `context.estimated_input_tokens` | 完整模型输入的调用前估算 |
| `context.input_budget_tokens` | 扣除输出预留与安全余量后的输入上限 |
| `context.before_tokens` / `context.after_tokens` | 本次缩减前后估算 |
| `context.reduction_kind` | 工具截断、清理或历史摘要等有限枚举 |
| `context.reduction_duration_ms` | 缩减耗时 |
| `context.summary_status` | 复用、更新、失效、失败或降级 |
| `context.artifact_bytes` | 临时工具结果存储量 |
| `context.artifact_expired_reads` | 临时结果过期后的读回次数 |
| `context.budget_rejections` | 最终输入无法满足预算的拒绝次数 |

所有指标只包含允许的元数据，不包含摘要正文、工具内容、主机路径或凭据。会话/Run ID 作为脱敏 Trace 关联字段，不作为高基数指标标签。摘要模型的 Usage 单独标识，避免与回答模型的用量混淆。

测试应验证每个工具循环 Step 都可观测预算检查，压缩前后指标口径一致，Trace 关闭时本地预算保护仍工作。CozeLoop 上报失败不触发额外压缩、改变摘要水位或放宽输入上限。

## 12. Server 接入流程

`cmd/server/main.go` 的装配逻辑调整为：

```text
cfg := config.Load()

observability, err := cozeloop.New(ctx, cfg.CozeLoop)
if err != nil {
    fail startup
}
if observability != nil {
    if cfg.CozeLoop.TraceEnabled {
        callbacks.AppendGlobalHandlers(observability.Handler())
    }
    defer observability.Close(shutdownCtx)
}

create chat model
create tools
create runtime builder
create executor
start HTTP server
```

当前 server 没有完整的 graceful shutdown 流程。接入 CozeLoop 时建议一并补上：

- 监听 `SIGINT` / `SIGTERM`；
- `server.Shutdown`；
- 停止接受新请求；
- 等待正在运行的聊天任务进入终态；
- 调用 CozeLoop client Close/flush；
- 最后关闭 SQLite。

如果本轮不想扩大范围，至少在 `main` 返回路径和测试装配中提供 `Close`，并记录 flush 错误。

## 13. Eval CLI 接入流程

`cmd/eval/main.go` 也应支持 CozeLoop，但与 server 分开初始化：

```text
load eval config
initialize optional CozeLoop once
create candidate model
create optional judge model
run selected cases / repeats
write local report
flush CozeLoop
```

评测 Trace 增加：

```text
run.mode=eval
run.id
case.id
case.version
case.tags
repeat.index
candidate.model
judge.enabled
```

Candidate 和 Judge 必须区分：

```text
agent.interview_review.candidate
agent.evaluation_judge
```

如果 Judge 也走 Eino ChatModel，它可以被 CozeLoop 追踪；但 Judge 的 Trace 应标记为 `judge`，避免与 Candidate 的工具调用混在一起。

## 14. 失败策略

Trace 上报失败不能影响 Agent 主链路；这不适用于固定版本 eval 的 Prompt 解析失败。
固定版本 eval 只能使用指定版本或同 Prompt 标识、工作空间、版本的缓存，禁止本地 fallback。
否则记为基础设施失败、不计质量分，不得剔除失败样本后宣称通过发布。
报告须记录 requested/resolved（含标识、工作空间、版本）、source、fallback、content hash、
有效/无效计数和发布资格；解析失败时 resolved/hash 可为空并说明原因。在线聊天可回退。
平台 API 对这些元数据与结果身份的映射尚待实现验证，不能视为已核实。

Trace 失败行为：

| 场景 | 行为 |
|---|---|
| CozeLoop 未配置或 Trace 独立开关关闭 | 正常运行，不注册 Trace Callback（其他启用功能仍校验各自配置） |
| CozeLoop 初始化失败 | 启用开关时启动失败；关闭时忽略 |
| Trace 上报失败 | 记录本地 warning，不影响模型调用 |
| CozeLoop 网络超时 | Callback 内部异步/超时处理，不能阻塞 Agent 主链路 |
| Close/flush 失败 | 记录 warning，不覆盖聊天最终状态 |
| Parser 失败 | 丢弃该条观测数据，不修改业务输出 |

不要在 Chat Executor 中捕获 CozeLoop 错误并改变 `streamErr`。

## 15. 与现有本地观测的关系

保留现有 `AGENT_DEBUG` 和 `graphDebugCallbacks`：

- `AGENT_DEBUG`：本地结构化日志，快速定位开发问题；
- CozeLoop：调用链、Token、跨 Run 聚合和可视化；
- 本地评测报告：确定性评分和 CI 产物。

不建议立即删除本地日志，因为：

- CozeLoop 可能未配置；
- 外部服务可能不可用；
- 本地日志适合健康检查和部署诊断；
- 单元测试不应依赖远程平台。

## 16. 测试策略

### 配置测试

- 默认关闭时不要求 CozeLoop 凭据；
- 启用但缺 Workspace ID 时返回配置错误；
- 启用但缺 API Token 时返回配置错误；
- API Token 不出现在错误字符串和日志中；
- Trace 独立开关和 `CaptureContent` 正确解析，默认均关闭，开关不代替授权。

### Adapter 单元测试

- `New` 在 disabled 状态不创建远程 client；
- `New` 正确创建 Handler；
- 自定义 Parser 覆盖无授权、有效授权的真实学习内容、超范围、撤销及接收平台变更重授权；
- 本地授权记录包含时间、范围和告知版本，告知已上传内容不会自动删除；
- 凭据在完整模式下仍被移除，错误始终脱敏，SDK Prompt Trace 无旁路泄露；
- 脱敏逻辑删除 API Key、Authorization 和路径信息；
- Close 可重复调用；
- Close 错误不会 panic。

### Eino 集成测试

- 使用本地 fake Callback Handler 验证 Handler 注册一次；
- Graph 的 model/tool callback 仍然执行；
- CozeLoop Handler 不改变文本流和 Tool Loop；
- 流式最后一个 usage-only chunk 不被丢弃；
- Agent 失败和取消都能生成 Trace 终态。

### Eval 测试

- `--mode validate` 不初始化 CozeLoop；
- live 模式只初始化一次；
- repeat 多次运行不会重复注册全局 Handler；
- run ID、case ID/version、tag、repeat index 能出现在 Trace 元数据；新运行身份不同，重传身份不变；
- 固定版本 eval 验证精确缓存命中和无缓存时基础设施失败，无本地 fallback；报告保留无效样本及发布不合格结论；
- CozeLoop 不可用时本地 report 仍正常写入。

## 17. 实施顺序

1. 锁定 `eino-ext/callbacks/cozeloop` 和 `cozeloop-go` 版本。
2. 扩展 `config.Config` 和环境变量解析。
3. 创建 `internal/infrastructure/observability/cozeloop` Adapter。
4. 先实现安全 Parser、独立脱敏 Trace 开关及完整内容授权校验，再接入 server。
5. 实现授权记录、撤销/重授权与所有上报出口的凭据过滤、错误脱敏，并验证 Graph/Tool/Model Trace。
6. 把 eval CLI 接入同一 Adapter，增加 run/case/version/repeat 元数据、严格版本解析与报告；核实平台 API 映射（待实现验证）。
7. 补充 CozeLoop client Close/flush 生命周期。
8. 运行本地 Agent、smoke eval 和 repeat eval，检查：
   - Trace 是否按 Run/Step/Tool 分层；
   - Token 是否与 Eino `ResponseMeta.Usage` 一致；
   - 流式输出是否未被 Callback 影响；
   - 评测报告是否仍正常生成。
9. 在 CI 中只在配置了 CozeLoop secrets 的受信任任务启用；普通 PR 不依赖远程平台。

## 18. CI 配置建议

新增可选 Secrets：

```text
COZELOOP_WORKSPACE_ID
COZELOOP_API_TOKEN
```

CI 行为：

- Go 单测不启用 CozeLoop；
- `validate` 不启用 CozeLoop；
- 受信任的 smoke/live job 可启用；
- CozeLoop 上报失败不应让 advisory smoke job 失败；
- 本地 `report.json` 和 `summary.md` 仍作为主评测产物上传。

## 19. 回滚方案

回滚只需要：

1. 设置 `COZELOOP_ENABLED=false`；
2. 停止注册全局 Handler；
3. 保留本地 Debug Callback 和评测报告；
4. 如需彻底移除，再回退两个 Go 依赖和 observability Adapter。

业务代码不应依赖 CozeLoop 类型，因此回滚不会影响 Agent、Tool、ContextManager 或 SQLite。

## 20. 最终建议

建议接入，但采用以下默认策略：

```text
开发环境：默认关闭 Trace；可独立启用脱敏 Trace
本地评测：默认关闭 Trace；可独立启用脱敏 Trace
普通单测：关闭
CI validate：关闭
受信任 smoke：可选启用
真实用户环境：默认关闭 Trace；可独立启用脱敏 Trace
所有环境完整内容：开关开启且用户明确授权，允许授权范围内真实学习内容
```

CozeLoop 集成第一阶段只观察现有运行链路，不让 CozeLoop 参与 ContextManager 或评测 Scorer 的决策。上下文预算和压缩可按专项方案独立推进，不以 Trace 上线为前置条件。调用链稳定后，可使用 Trace 数据校准：

- 上下文 Token 预算；
- 工具调用耗时；
- Judge 和 Candidate 延迟；
- 超时阈值；
- 评测 case 的稳定性。
