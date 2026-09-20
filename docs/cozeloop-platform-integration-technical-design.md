# CozeLoop 平台集成技术方案

- 状态：Proposal
- 目标：将 CozeLoop 的 Trace、Prompt 开发与版本管理、评测集、评估器和实验能力集成到当前 Go/Eino 面试复盘 Agent。
- 实施原则：分阶段交付，每一步均可独立验证和回滚；CozeLoop 不进入领域业务逻辑，不替代 Eino Graph，也不影响未启用时的本地运行。

## 1. 背景

当前项目已经具备：

- Go + Eino Agent Graph；
- ChatModel 流式输出；
- Tool Calling 循环；
- 题库、作答和复盘工具；
- 本地 JSONL 评测集；
- 确定性工具行为评分；
- 独立 LLM Judge；
- JSON/Markdown 评测报告；
- 本地 Graph Debug Callback；
- 上下文管理代码骨架。

当前缺少统一的平台能力：

1. 无法可视化分析一次 Agent Run 中的 Graph、模型和工具调用链；
2. Prompt 硬编码在 Go 中，修改必须重新编译；
3. Prompt 缺少 Playground 调试、版本管理和标签发布；
4. 本地评测结果缺少实验管理、人工校准和跨版本对比；
5. Trace、Prompt 版本和评测结果之间尚未建立关联。

CozeLoop 可以补齐上述平台能力，但必须与本地 Agent 的执行边界保持清晰。

## 2. 总体目标

完整集成后形成以下闭环：

```text
CozeLoop Prompt Playground
        |
        | 提交 Prompt 版本
        v
CozeLoop Prompt Hub
        |
        | Go SDK 拉取指定版本/标签
        v
本地 Go/Eino Agent
        |
        +-- Eino Graph
        +-- ChatModel
        +-- Tool Calling
        +-- ContextManager
        |
        +------> CozeLoop Trace
        |
        v
本地 Eval Runner
        |
        +-- 确定性工具断言
        +-- actual_output
        +-- tool_trace
        +-- duration / token / trace_id
        |
        v
CozeLoop 评测集与实验
        |
        +-- LLM 评估器
        +-- Code 评估器
        +-- 人工校准
        +-- 多实验对比
        |
        v
Prompt / Agent 改进
```

## 3. 能力边界

### 3.1 CozeLoop 负责

- Eino Graph、ChatModel 和 Tool 的调用追踪；
- Prompt Playground 调试；
- Prompt 版本管理和标签发布；
- 评测集及其版本管理；
- LLM/Code 评估器；
- 评测实验和聚合分析；
- 人工评分校准；
- 多实验横向对比；
- Token、耗时、错误和 Trace 联动分析。

### 3.2 本地项目继续负责

- Eino Graph 结构；
- 工具注册、参数校验和调用上限；
- SQLite fixture 注入；
- 自建 Go/Eino Agent 的实际执行；
- 上下文裁剪和摘要；
- 工具调用硬断言；
- 关键失败规则；
- 本地 CI 最低安全门禁；
- 用户消息和业务数据持久化。

### 3.3 不允许下沉到 Prompt 的规则

以下规则必须保留在 Go 代码中：

- 工具参数合法性；
- 最大工具循环次数；
- 禁止调用的工具；
- 用户确认后才写入长期记忆；
- Token 上下文硬限制；
- SQLite 事务和数据完整性；
- 取消、超时和重试边界；
- 凭据与日志安全。

## 4. 集成模块划分

建议新增以下模块：

```text
backend/internal/infrastructure/cozeloop/
  client.go                # Client 生命周期与统一配置
  tracing.go               # Eino Callback Handler
  prompt_provider.go       # Prompt Hub Adapter
  evaluation_client.go     # 评测集和实验 OpenAPI Adapter
  data_parser.go           # Trace 输入输出解析和脱敏
  types.go                 # 基础设施层 DTO

backend/internal/agent/prompt/
  provider.go              # Agent 使用的 PromptProvider seam
  local_provider.go        # 本地 Prompt fallback
  resolver.go              # 远程/缓存/本地选择策略

backend/internal/eval/cozeloop/
  publisher.go             # 本地评测输出同步
  mapper.go                # EvalCase/CaseResult 字段映射
  experiment.go            # 提交和查询实验
```

依赖方向：

```text
application / agent
       |
       v
PromptProvider interface
       ^
       |
infrastructure/cozeloop Adapter

local eval runner
       |
       v
eval/cozeloop Publisher
       |
       v
infrastructure/cozeloop OpenAPI Client
```

业务层不得直接引用 CozeLoop SDK 类型。

## 5. 配置设计

扩展配置：

```go
type CozeLoopConfig struct {
    Enabled         bool
    WorkspaceID     string
    APIToken        string
    Environment     string
    ServiceName     string
    CaptureContent  bool
    PromptEnabled   bool
    EvaluationEnabled bool
    PromptCacheSize int
    PromptRefreshInterval time.Duration
}
```

环境变量：

```text
COZELOOP_ENABLED=false
COZELOOP_WORKSPACE_ID=
COZELOOP_API_TOKEN=
COZELOOP_ENVIRONMENT=local
COZELOOP_SERVICE_NAME=interview-memory-agent
COZELOOP_CAPTURE_CONTENT=true
COZELOOP_PROMPT_ENABLED=false
COZELOOP_EVALUATION_ENABLED=false
COZELOOP_PROMPT_CACHE_SIZE=100
COZELOOP_PROMPT_REFRESH_INTERVAL=1m
```

Prompt 配置：

```text
AGENT_PROMPT_KEY=interview-review-agent
AGENT_PROMPT_VERSION=
AGENT_PROMPT_LABEL=development

JUDGE_PROMPT_KEY=interview-review-judge
JUDGE_PROMPT_VERSION=

SUMMARY_PROMPT_KEY=interview-conversation-summary
SUMMARY_PROMPT_VERSION=
```

规则：

- `COZELOOP_ENABLED=false` 时不要求任何 CozeLoop 凭据；
- Trace、Prompt 和 Evaluation 可以分别启用；
- 评测和 CI 必须指定具体 Prompt Version；
- Version 非空时忽略 Label；
- 日常开发可使用 `development` 标签；
- 正常运行可使用 `production` 标签；
- API Token 不得出现在错误、日志、Trace 或报告中。

## 6. Prompt 管理设计

### 6.1 PromptProvider seam

```go
type Provider interface {
    Resolve(context.Context, Request) (ResolvedPrompt, error)
}

type Request struct {
    Key       string
    Version   string
    Label     string
    Variables map[string]any
}

type ResolvedPrompt struct {
    Key      string
    Version  string
    Label    string
    Source   Source
    Messages []*schema.Message
}
```

`Source`：

```text
cozeloop
cache
local
```

### 6.2 Provider 实现

```text
LocalPromptProvider
CozeLoopPromptProvider
FallbackPromptProvider
```

`FallbackPromptProvider` 的决策：

```text
CozeLoop 拉取成功
  -> 使用远程 Prompt

远程失败但 SDK 缓存可用
  -> 使用缓存 Prompt

远程失败且无缓存
  -> 使用本地内置 Prompt
```

每次 Resolve 必须返回最终具体版本，以便 Trace 和评测记录。

### 6.3 首批 Prompt

| Prompt Key | 用途 | 优先级 |
|---|---|---:|
| `interview-review-agent` | Agent 主指令 | P0 |
| `interview-review-judge` | LLM Judge | P1 |
| `interview-conversation-summary` | 会话摘要 | P1 |

### 6.4 Agent 主 Prompt 变量

建议 Prompt Hub 模板包含：

```text
conversation_summary
interview_context
chat_history
user_query
```

工具 Schema 不放入 Prompt Hub，由 Eino `WithTools` 绑定。

### 6.5 Playground 的定位

Playground 用于：

- 单轮 Prompt 调试；
- 不同模型和参数比较；
- Prompt 变量格式化验证；
- Judge 规则调试；
- 摘要 JSON 输出调试。

Playground 不验证：

- 完整 Tool Loop；
- SQLite fixture；
- 工具参数和顺序；
- Graph 取消与重连；
- 多轮上下文持久化。

完整行为仍需通过本地 Agent Runner 和 CozeLoop 实验验证。

## 7. Prompt 版本发布策略

```text
编辑草稿
  -> Playground 调试
  -> 提交不可变版本
  -> 本地 Runner 固定版本运行
  -> CozeLoop 实验
  -> 人工检查 badcase
  -> 标签切换 development / production
```

版本规则：

- 已提交版本不可原地修改；
- 评测记录必须保存具体版本；
- `latest` 仅允许开发调试，不允许 CI；
- 标签切换不等同于创建新版本；
- production 标签切换前必须有通过的评测实验；
- 回滚只需将 production 标签重新指向旧版本。

## 8. Trace 设计

Trace 层级：

```text
interview_review run
  +-- prompt.resolve
  +-- prepare_context
  +-- chat_template
  +-- chat_model step-1
  +-- tools
  |    +-- search_question_memory
  |    +-- get_question_context
  +-- chat_model step-2
  +-- final output
```

记录字段：

```text
service.name
service.environment
agent.name
conversation.id
assistant.message.id
model.name
prompt.key
prompt.version
prompt.label
prompt.source
case.id
case.tags
run.mode
step.id
tool.name
status
error.kind
duration_ms
input_tokens
output_tokens
total_tokens
```

`case.*` 只在评测 Runner 中存在。

### Token 分工

```text
TokenEstimator
  -> 模型调用前预算和裁剪

Eino ResponseMeta.Usage + CozeLoop Callback
  -> 模型调用后实际 Token 统计
```

Tool Loop 的每次 ChatModel Step 单独记录 Usage，并在 Run 级别聚合。

## 9. CozeLoop 评测集设计

建议建立评测集：

```text
interview-agent-evaluation
```

字段：

```text
case_id
case_version
tags
input_query
interview_context
history
fixtures
expected_tools
expected_arguments
expected_order
required_facts
forbidden_content
judge_rubric

actual_output
tool_trace
execution_status
execution_error
duration_ms
prompt_tokens
completion_tokens
total_tokens
trace_id
prompt_key
prompt_version
candidate_model

local_deterministic_score
local_hard_failure
```

字段分组：

- 输入：原始 EvalCase；
- 期望：工具和答案断言；
- 输出：本地 Agent 执行结果；
- 指标：Token、耗时、Trace；
- 本地结论：确定性分数和关键失败。

## 10. 评估器设计

### 10.1 本地确定性 Scorer

继续保留：

- 必需工具；
- 禁止工具；
- 工具参数；
- 调用顺序；
- 最大调用次数；
- 超时；
- 空回答；
- 关键失败。

### 10.2 CozeLoop Code 评估器

迁移或复制：

- 必须包含内容；
- 禁止内容；
- JSON 格式检查；
- 非空输出；
- 状态是否成功；
- Tool Trace 的简单结构判断。

Code 评估器适合精确规则，但不能替代 Go Runner 对 Graph 内部行为的实时检查。

### 10.3 CozeLoop LLM 评估器

建立四个维度：

```text
groundedness
instruction_following
completeness
actionability
```

推荐分开建立四个评估器，便于：

- 独立调试；
- 独立版本管理；
- 识别具体退化维度；
- 人工校准。

初期也可保留一个综合评估器，待数据稳定后再拆分。

## 11. 自建 Agent 的评测执行方式

### 第一阶段：本地执行 + 输出回流

```text
JSONL
  -> Go Eval Runner
  -> 自建 Eino Agent
  -> actual_output/tool_trace/usage
  -> CozeLoop Evaluation Set
  -> 无评测对象实验
  -> Evaluators
```

CozeLoop 官方实验支持不设置评测对象，直接使用评测集中的实际输出执行评估器。该方式最适合当前项目。

### 第二阶段：自定义评测对象

如果当前 CozeLoop 服务和账号开放 Custom RPC/A2A/Sandbox Agent：

```text
CozeLoop Experiment
  -> 调用 Agent Endpoint
  -> Go/Eino Agent
  -> 上报输出、Usage、错误和 Step Metric
```

该阶段需要部署可访问 Endpoint、鉴权、幂等和并发控制，不作为首期范围。

## 12. OpenAPI Adapter

`EvaluationClient` 接口：

```go
type EvaluationClient interface {
    EnsureDataset(context.Context, DatasetSpec) (Dataset, error)
    BatchUpsertItems(context.Context, DatasetID, []DatasetItem) error
    CreateVersion(context.Context, DatasetID, VersionSpec) (DatasetVersion, error)
    SubmitExperiment(context.Context, ExperimentSpec) (Experiment, error)
    GetExperiment(context.Context, ExperimentID) (Experiment, error)
    ListExperimentResults(context.Context, ExperimentID) ([]Result, error)
}
```

首期只实现本项目需要的最小 OpenAPI 集合，不在业务代码中暴露 CozeLoop 原始请求结构。

幂等键：

```text
dataset_key = interview-agent-evaluation
item_key = case_id + case_version
experiment ext = git_commit + prompt_version + model
```

## 13. Eval CLI 扩展

建议新增参数：

```text
--publish-cozeloop
--cozeloop-dataset-key
--cozeloop-experiment-name
--prompt-version
--prompt-label
--wait-experiment
```

示例：

```bash
go run ./cmd/eval \
  --mode live \
  --tag smoke \
  --prompt-version 1.2.0 \
  --publish-cozeloop \
  --wait-experiment
```

执行流程：

```text
加载数据集
  -> 校验 Prompt Version
  -> 初始化 CozeLoop Trace
  -> 执行 Candidate
  -> 执行本地 Scorer
  -> 生成本地 Report
  -> 同步 CozeLoop 数据项
  -> 创建评测集版本
  -> 提交实验
  -> 可选等待实验完成
  -> 输出本地报告路径和 Experiment ID/URL
```

## 14. 分步骤实施计划

## Step 1：CozeLoop 基础 Client 与配置

### 目标

建立所有后续能力共享的 CozeLoop Client 生命周期，不接入 Trace、Prompt 或评测业务。

### 工作内容

1. 扩展 `config.Config`，增加 `CozeLoopConfig`；
2. 解析环境变量和布尔/时长配置；
3. 校验启用状态和凭据；
4. 新增 `internal/infrastructure/cozeloop/client.go`；
5. 封装 SDK Client 创建与 Close；
6. 禁止业务层直接创建 SDK Client；
7. 增加配置与 Client 单元测试。

### 完成标准

- 默认关闭时不要求凭据；
- 开启但缺少凭据时启动失败；
- Token 不出现在错误和日志；
- Client 可重复安全关闭；
- `go test ./...` 通过。

## Step 2：Eino Trace 接入

### 目标

在不改变 Agent 行为的前提下，将 Graph、模型和工具调用上传到 CozeLoop。

### 工作内容

1. 引入 `eino-ext/callbacks/cozeloop`；
2. 创建 Loop Handler；
3. 在 server 应用装配阶段注册一次全局 Handler；
4. 保留现有本地 Graph Debug Callback；
5. 配置 `CaptureContent`；
6. 记录 Run、Step、Tool、Token 和错误；
7. 增加 client Close/flush；
8. 验证流式 Usage-only chunk 不丢失。

### 完成标准

- 一次聊天在 CozeLoop 中展示完整调用链；
- Tool Loop 层级正确；
- 每个模型 Step 有 Token 和耗时；
- CozeLoop 上报失败不影响聊天；
- 关闭开关后行为与当前一致。

## Step 3：PromptProvider seam 与本地 Provider

### 目标

先解耦 Agent 与硬编码 Prompt，再接远程 Prompt Hub。

### 工作内容

1. 定义 `PromptProvider`、`Request`、`ResolvedPrompt`；
2. 实现 `LocalPromptProvider`；
3. 将 `systemInstruction` 移入 Local Provider；
4. Builder 通过 Provider 获取 Prompt；
5. 保持默认行为完全一致；
6. 为变量缺失、消息角色和格式增加测试。

### 完成标准

- 未启用 CozeLoop 时输出与当前一致；
- Builder 不再直接依赖 Prompt 常量；
- 单元测试不访问远程平台；
- Prompt 来源可以被 Trace 记录。

## Step 4：CozeLoop Prompt Hub 接入

### 目标

支持从 CozeLoop 拉取指定 Prompt 版本或标签，并保留本地回退。

### 工作内容

1. 实现 `CozeLoopPromptProvider`；
2. 调用 `GetPrompt` 和 `PromptFormat`；
3. 将 CozeLoop Message 转换为 Eino `schema.Message`；
4. 支持 Version 和 Label；
5. 配置 SDK Prompt Cache；
6. 实现 `FallbackPromptProvider`；
7. Trace 记录最终 Prompt Key/Version/Source；
8. 创建 `interview-review-agent` 首个版本。

### 完成标准

- development 标签可热切换；
-固定 Version 可重复运行；
- 远程不可用时使用缓存或本地 Prompt；
- 实际版本出现在 Trace 和评测报告；
- Tool Schema 仍由本地代码绑定。

## Step 5：Playground 调试与版本发布流程

### 目标

建立团队可重复执行的 Prompt 编辑、调试、评测和发布流程。

### 工作内容

1. 定义 Prompt 变量 Schema；
2. 在 Playground 构造典型测试输入；
3. 覆盖直接回答、检索、无结果、工具失败和对抗场景；
4. 提交不可变 Prompt 版本；
5. 设置 development 标签；
6. 通过本地 Runner 固定版本执行；
7. 评测通过后移动 production 标签；
8. 记录回滚方法。

### 完成标准

- 每次 Prompt 修改都有新版本；
- production 标签只指向已评测版本；
- 可以一键切回旧版本；
- Playground 测试数据和正式评测 case 有明确对应关系。

## Step 6：Eval Runner 的 Trace 元数据

### 目标

让每个评测 case 的本地执行都可以关联到 CozeLoop Trace。

### 工作内容

1. Eval CLI 初始化 CozeLoop；
2. 每个 case 创建稳定 Trace 元数据；
3. 增加 `case_id`、tag、repeat index；
4. 区分 Candidate 和 Judge；
5. 收集 Trace ID 和实际 Usage；
6. 将 Trace ID 写入 `CaseResult`；
7. 本地报告展示 Experiment/Trace 引用。

### 完成标准

- 每个 case 可定位到 Trace；
- Candidate/Judge Trace 不混淆；
- repeat 运行可以独立区分；
- 未启用 CozeLoop 时本地报告仍正常。

## Step 7：评测集同步

### 目标

将现有 JSONL case 和本地执行结果同步到 CozeLoop 评测集。

### 工作内容

1. 实现 `EvaluationClient` 的评测集接口；
2. 创建固定 `dataset_key`；
3. 映射 EvalCase 字段；
4. 映射 actual_output、tool_trace、Usage 和 Trace ID；
5. 批量写入或更新数据项；
6. 创建不可变评测集版本；
7. 保证按 case ID 幂等。

### 完成标准

- 24 条本地 case 可完整同步；
- 重复发布不会产生不可控重复数据；
- CozeLoop 数据项可以查看输入、期望、实际输出和 Trace；
- 本地 JSONL 仍然可以独立运行。

## Step 8：CozeLoop Code 评估器

### 目标

迁移适合平台执行的精确规则，形成可视化指标。

### 工作内容

1. 创建非空回答评估器；
2. 创建必须包含内容评估器；
3. 创建禁止内容评估器；
4. 创建执行状态评估器；
5. 可选创建 Tool Trace 结构评估器；
6. 使用样例数据调试；
7. 提交评估器版本。

### 完成标准

- Code 评估器结果与本地对应断言一致；
- 差异 case 有明确原因；
- 评估器版本固定；
- 不删除本地关键失败规则。

## Step 9：CozeLoop LLM 评估器

### 目标

将回答质量评分迁移到可版本化、可人工校准的评估器。

### 工作内容

1. 创建忠实度评估器；
2. 创建指令遵循评估器；
3. 创建完整性评估器；
4. 创建可执行性评估器；
5. 映射 input、context、actual_output、reference facts；
6. 使用已有 Judge 样例调试；
7. 提交评估器版本；
8. 对 10～15 个 case 进行人工校准。

### 完成标准

- 每个维度有独立分数和理由；
- 评估器版本可追踪；
- 人工评分与 LLM 评分差异被记录；
- 未校准前不作为 CI 硬门禁。

## Step 10：自动提交评测实验

### 目标

通过 CLI 自动创建 CozeLoop 实验，无需手工上传和点击页面。

### 工作内容

1. 实现 SubmitExperiment；
2. 选择固定评测集版本；
3. 不设置评测对象，使用评测集 actual_output；
4. 绑定 Code/LLM 评估器版本；
5. 映射所有评估器字段；
6. 支持轮询实验状态；
7. 输出 Experiment ID/URL；
8. 将实验信息写入本地 summary。

### 完成标准

- 一条 CLI 命令完成本地执行、同步和实验提交；
- 实验结果可在 CozeLoop 查看；
- 本地 Report 和平台结果可通过 case ID 对齐；
- 平台失败不删除本地报告。

## Step 11：Prompt A/B 与多实验对比

### 目标

使用相同评测集和评估器比较不同 Prompt 版本。

### 工作内容

1. 固定模型、参数、工具和 Context 策略；
2. 分别运行 Prompt vA、vB；
3. 提交两个实验；
4. 在 CozeLoop 建立实验对比；
5. 对比质量、Token、耗时和 badcase；
6. 选择候选版本；
7. 将 production 标签切换到胜出版本。

### 完成标准

- 两个实验只有 Prompt Version 不同；
- 可以逐 case 对比输出和评分；
- 可以查看总体指标变化；
- 发布决定有实验依据。

## Step 12：CI 集成

### 目标

在受信任环境中自动运行 smoke 评测并可选发布 CozeLoop 实验。

### 工作内容

1. 增加 CozeLoop Secrets；
2. PR validate 不连接 CozeLoop；
3. 主分支或手工任务运行 live smoke；
4. 固定 Prompt Version；
5. 上传本地报告；
6. 可选提交 CozeLoop 实验；
7. 输出实验链接；
8. 校准完成后再决定门禁阈值。

### 完成标准

- 普通单测不依赖 CozeLoop；
- Secrets 缺失时明确跳过；
- 本地报告始终保留；
- CozeLoop 评测初期为 advisory；
- 启用门禁需要单独评审。

## 15. 测试策略

### 单元测试

- 配置解析和验证；
- Prompt Provider fallback；
- CozeLoop/Eino Message 转换；
- EvalCase 字段映射；
- OpenAPI 错误分类；
- Token 和 Trace 元数据聚合；
- 凭据不进入错误或日志。

### 集成测试

- 使用 fake CozeLoop Client；
- 验证 Handler 仅注册一次；
- 验证 Prompt 固定版本；
- 验证远程失败回退本地 Prompt；
- 验证评测集批量同步和幂等；
- 验证本地报告在平台失败时仍写入。

### 手工验收

- Playground 比较两个 Prompt；
- Trace 中查看完整 Tool Loop；
- 实验中查看 Code/LLM 评估器；
- 人工校准错误评分；
- 多实验逐行对比；
- production 标签切换和回滚。

## 16. 风险与缓解

| 风险 | 缓解措施 |
|---|---|
| Prompt Hub 不可用 | SDK 缓存 + 本地 Prompt fallback |
| 标签导致版本漂移 | 评测和 CI 固定 Version |
| Playground 与真实 Agent 不一致 | 完整 Agent 必须跑本地 Runner |
| 平台评估器误判 | 保留本地 Scorer + 人工校准 |
| Trace Callback 影响流式输出 | 使用 Eino stream copy，集成测试验证 |
| OpenAPI 版本变化 | 通过内部 Adapter 隔离原始 DTO |
| CozeLoop 失败阻塞聊天 | Trace 上报失败不改变业务错误 |
| 平台结果不可复现 | 保存 Git Commit、Prompt Version、模型和数据集版本 |
| LLM 评估成本增加 | smoke 子集、并发限制、按需实验 |

## 17. 回滚策略

### Trace 回滚

```text
COZELOOP_ENABLED=false
```

### Prompt 回滚

- production 标签切回旧版本；或
- `COZELOOP_PROMPT_ENABLED=false` 使用本地 Prompt。

### 评测回滚

- 停止 `--publish-cozeloop`；
- 本地 JSONL Runner 和报告继续工作；
- 不删除已有实验数据。

## 18. 推荐实施顺序总结

```text
Step 1  基础 Client/配置
Step 2  Trace
Step 3  PromptProvider seam
Step 4  Prompt Hub
Step 5  Playground/发布流程
Step 6  Eval Trace 元数据
Step 7  评测集同步
Step 8  Code 评估器
Step 9  LLM 评估器
Step 10 自动实验
Step 11 Prompt A/B
Step 12 CI
```

依赖关系：

```text
1 -> 2
1 -> 3 -> 4 -> 5
2 + 4 -> 6
6 -> 7 -> 8/9 -> 10 -> 11 -> 12
```

Trace 和 Prompt seam 可以在 Step 1 后并行实施；评测集同步依赖 Eval Runner 已经能记录 Trace 和 Prompt Version。

## 19. 首期建议范围

首期建议完成 Step 1～Step 5：

- CozeLoop Client；
- Eino Trace；
- PromptProvider；
- 主 Agent Prompt Hub；
- Playground 和 Prompt 版本发布流程。

第二期完成 Step 6～Step 10：

- 评测 Trace；
- 评测集同步；
- Code/LLM 评估器；
- 自动实验。

第三期完成 Step 11～Step 12：

- Prompt A/B；
- CI 门禁与持续评测。

这样可以先获得调用链和 Prompt 版本管理价值，再逐步迁移评测工作台，不会一次性改动 Agent 执行、Prompt 和评测三条主链路。
