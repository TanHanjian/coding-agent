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
    TraceEnabled    bool
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
COZELOOP_TRACE_ENABLED=false
COZELOOP_CAPTURE_CONTENT=false
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
- Trace、Prompt 和 Evaluation 可以分别启用；脱敏 Trace 使用独立的 `COZELOOP_TRACE_ENABLED`，默认关闭，不能随 Prompt 启用而隐式开启；
- 所有环境默认无完整 Trace；`CaptureContent` 不是授权，完整内容须同时满足开关与第 8 节授权规则；
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
    Key       string
    Version   string
    Label     string
    Source    Source
    Templates []schema.MessagesTemplate
}
```

`Templates` 使用 Eino 的 `schema.MessagesTemplate`，因为 Prompt 既可能包含普通消息模板，也可能包含 `MessagesPlaceholder` 这类运行时历史占位符。

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

`FallbackPromptProvider` 必须区分在线聊天与固定版本 eval：

```text
在线聊天：远程 -> 可用缓存 -> 本地内置 Prompt（记录 fallback）
固定版本 eval：指定版本 -> 同 Prompt 标识/工作空间/版本的缓存
              -> 均不可用或解析/格式化/转换失败：基础设施失败，禁止本地 fallback
```

固定版本 eval 不得使用其他版本、标签最新值或其他工作空间缓存；失败样本不计质量分，
但必须计入无效计数，不得剔除后以剩余样本通过率得出通过发布结论。
报告必须记录 `requested`、`resolved`（均含 Prompt 标识、工作空间、版本）、`source`、
`fallback`、`content_hash`（实际解析模板内容的哈希）、有效/无效计数及发布资格。
未解析成功时 resolved/hash 为空并记录脱敏失败原因，不得伪装成请求版本成功。
该约束覆盖本次评测使用的 Agent、Judge 和摘要 Prompt；在线聊天仍可回退。
每次成功 Resolve 返回实际具体版本和来源，失败也保留请求元数据。

### 6.3 首批 Prompt

| Prompt Key | 用途 | 优先级 |
|---|---|---:|
| `interview-review-agent` | Agent 主指令 | P0 |
| `interview-review-judge` | LLM Judge | P1 |
| `interview-conversation-summary` | 会话摘要 | P1 |

### 6.4 Agent 主 Prompt 变量

Prompt Hub 模板必须使用以下变量名：

```text
history                 placeholder
query                   string
interview_context       string
conversation_summary    string
```

权威 Schema 位于 `evals/interview-agent-playground.v1.json`。

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
- production 标签切换前必须有具备发布资格且通过的固定版本评测实验；基础设施失败导致无效样本时本次运行不具备发布资格，不得剔除失败样本后发布；
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

`case.*` 只在评测 Runner 中存在。评测还记录 `run.id`、`case.version`、`repeat.index`，
以及 requested/resolved/source/fallback/content hash；身份规则见第 12 节。

### 内容与授权边界

所有环境默认不上传完整 Trace；独立启用的脱敏 Trace 仅含允许的元数据，不含内容摘要。
用户明确授权后允许真实完整学习内容，不限于 fixture，包括授权范围内的 Prompt、学习输入输出、
Tool 参数/结果和摘要。`CaptureContent=true` 只是技术开关，不代替授权。
授权必须明确目的、接收平台（含工作空间）和内容范围；本地记录授权时间、范围、告知版本。
撤销立即停止后续完整上报（含尚未发送的队列内容），已上传内容不会自动删除，须事先告知；
接收平台或内容范围变化必须重新授权。无授权、撤销或超范围时不得继续完整上报。
凭据永不作为内容上传，错误始终脱敏；SDK Prompt Trace、Candidate/Judge Trace、评测集和实验同步
均须受同一授权与过滤边界约束，不能因启用其他功能而绕过。

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

- 输入：原始 EvalCase，稳定身份为 `case_id + case_version`；
- 期望：工具和答案断言；
- 输出：本地 Agent 执行结果，独立携带 `run_id`、`case_id`、`case_version`、`repeat_index`，不得按 case identity 覆盖跨运行结果；
- 指标：Token、耗时、Trace；
- 本地结论：确定性分数、关键失败、基础设施状态、有效/无效计数和发布资格；
- 解析审计：requested/resolved/source/fallback/content hash（见第 6.2 节），commit/model/prompt 是元数据而非运行身份。

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

首期拟采用不设置评测对象、直接以评测集 actual_output 执行评估器的方式。
目标平台版本/账号的 API 支持、字段映射和身份语义仍须在实施时验证，本文不声称已核实。

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

本地身份及幂等约定（不是已核实的平台 API 字段）：

```text
dataset_key = interview-agent-evaluation
case_identity = case_id + case_version
run_id = 每次新运行生成的唯一 ID
result_identity = run_id + case_id + case_version + repeat_index
experiment_identity = run_id
metadata = git_commit + model + prompt 标识/版本/来源
```

case identity 用于稳定输入定义；run/execution 用于具体执行，二者分离。同一结果/实验重传复用
原 run_id 和 result_identity；相同 commit/model/prompt 的新运行仍生成新 run_id。
执行输出不得以 case identity upsert 而覆盖不同运行或 repeat。

待实现验证：核实平台 dataset item 外部键、不可变版本、实验外部 ID、重复提交及结果关联 API 的
实际语义，并完成上述身份到平台字段的映射测试。必要时采用按运行的输出数据项/快照和本地映射表；
未完成验证前不宣称平台天然支持这些幂等键。

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
  -> 生成新 run_id，建立 case/version/repeat 结果身份
  -> 按独立配置初始化脱敏 Trace，完整内容另检查授权
  -> 严格解析固定 Prompt 版本（失败记基础设施无效，不执行质量评分）
  -> 执行 Candidate
  -> 对有效结果执行本地 Scorer
  -> 生成含解析审计、有效/无效计数及发布资格的本地 Report
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
5. 实现独立 Trace 开关、`CaptureContent` 与用户授权校验、授权本地记录和撤销/重授权；
6. 记录 Run、Step、Tool、Token 和错误；
7. 增加 client Close/flush；
8. 验证流式 Usage-only chunk 不丢失。

### 完成标准

- 独立启用脱敏 Trace 后展示调用层级，默认无完整内容；
- 用户明确授权后可上传范围内真实学习内容，覆盖无授权/撤销/平台或范围变更测试；
- 凭据永不上传，错误始终脱敏，SDK Prompt Trace 无旁路；
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

支持从 CozeLoop 拉取指定 Prompt 版本或标签；在线聊天保留本地回退，固定版本 eval 严格解析。

### 现有实现记录（不代表以下新约束已验收）

1. `backend/internal/infrastructure/cozeloop/prompt_provider.go` 实现
   `CozeLoopPromptProvider`；
2. 调用 SDK 的 `GetPrompt` 和 `PromptFormat`；
3. 将远程文本 Message 转换为 Eino `schema.Message`；
4. 支持固定 `Version` 或 `Label`，固定版本优先；
5. 在 CozeLoop Client 初始化时配置 Prompt Cache；
6. 现有远程获取、格式化或转换失败路径回退 `LocalPromptProvider`；待修订为仅在线聊天允许，固定版本 eval 必须按第 6.2 节失败；
7. 将最终 Prompt Key、Version、Label 和 Provider 写入 Eino Message metadata，
   供 Trace 解析；
8. `Builder` 将 ContextManager 准备好的 history、query、interview context 和
   conversation summary 作为 Prompt Hub 变量传入；
9. Server 和 Eval CLI 只有在 `COZELOOP_PROMPT_ENABLED=true` 时选择远程 Provider。

### 配置

```text
COZELOOP_PROMPT_ENABLED=false
AGENT_PROMPT_KEY=interview-review-agent
AGENT_PROMPT_VERSION=
AGENT_PROMPT_LABEL=development
COZELOOP_PROMPT_CACHE_SIZE=100
COZELOOP_PROMPT_REFRESH_INTERVAL=10m
```

`COZELOOP_PROMPT_ENABLED=true` 要求同时启用 CozeLoop Client。未启用时，默认
在线聊天仍使用内置 `LocalPromptProvider`；请求远程固定版本的 eval 不得借关闭开关改用本地。
`COZELOOP_CAPTURE_CONTENT` 默认是 `false`，设为 `true` 仍须独立 Trace 开关及有效用户授权。
`PromptFormat` 在 SDK 本地执行；SDK Prompt Trace 必须同样受授权、撤销及过滤控制。

### 待实施修订

- 为 eval 增加严格解析模式和同标识/工作空间/版本缓存校验；解析失败记基础设施失败；
- 为解析结果/报告补齐 requested/resolved/source/fallback/content hash、有效/无效计数和发布资格；
- 实现独立脱敏 Trace 配置与逐次完整内容授权检查，不能仅凭 CaptureContent 开启 SDK Prompt Trace。

### 完成标准

- development 标签可以在 SDK Cache 刷新后获取新版本；
- 固定 Version 可以重复运行；
- 在线聊天远程不可用时可使用缓存或本地 Prompt；固定版本 eval 仅接受精确匹配缓存，否则基础设施失败且不计质量分；
- 无效样本仍保留并阻止本次运行通过发布，报告包含全部解析审计字段和计数；
- Prompt 元数据和 `prompt_fallback` 状态出现在 Eino/CozeLoop Trace 的可关联 metadata 中；
- fallback 会输出不包含远程错误原文、Prompt 内容或 Token 的结构化 warning；
- Tool Schema 仍由本地代码绑定；
- Prompt Provider 单元测试不访问远程平台。

### 尚需平台侧操作

代码不会替账号在 CozeLoop 工作空间创建 Prompt。需要在 Prompt Hub 中创建
`interview-review-agent`，声明以下变量并发布首个版本：

```text
history                 placeholder
query                   string
interview_context       string
conversation_summary    string
```

## Step 5：Playground 调试与版本发布流程

### 目标

建立团队可重复执行的 Prompt 编辑、调试、评测和发布流程。

### 已实施的仓库侧能力

1. 新增 `evals/interview-agent-playground.v1.json`，定义 Prompt 变量 Schema；
2. 将 direct、retrieval、context、missing、failure 和 adversarial case 映射为 Playground 场景；
3. Eval CLI 新增 `validate-playground`，校验变量、场景和 case 一对一覆盖；
4. Eval CLI 支持 `--prompt-version`、`--prompt-label`；
5. `--require-prompt-version` 阻止发布前评测使用 Label、`latest` 或未固定版本；
6. 本地报告记录本次请求的 Prompt Key、Version、Label 和 Source；
7. 新增 `docs/prompt-playground-release.md`，定义开发、固定版本评测、production 标签和回滚流程。

### 平台侧操作

Prompt 草稿编辑、不可变版本提交、development/production 标签移动和回滚仍在
CozeLoop 控制台完成。代码不会自动移动生产标签。

### 完成标准

- Prompt 变量 Schema 与 Agent 实际变量一致；
- Playground 场景覆盖正式评测 case；
- 发布前评测可强制使用具体 Prompt Version；
- production 标签只指向已评测版本；
- 可以按文档回滚到旧版本；
- Playground 测试数据和正式评测 case 有明确对应关系。

## Step 6：Eval Runner 的 Trace 元数据

### 目标

让每个评测 case 的本地执行都可以关联到 CozeLoop Trace。

### 工作内容

1. Eval CLI 初始化 CozeLoop；
2. 每个 case 创建稳定 Trace 元数据；
3. 增加 `run_id`、`case_id`、`case_version`、tag、repeat index；每次新运行生成新 run_id；
4. 区分 Candidate 和 Judge；
5. 收集 Trace ID 和实际 Usage；
6. 将 Trace ID 写入 `CaseResult`；
7. 本地报告展示 Experiment/Trace 引用。

### 完成标准

- 每个 case 可定位到 Trace；
- Candidate/Judge Trace 不混淆；
- 新运行和 repeat 可独立区分，同一结果重传复用原身份，commit/model/prompt 仅作元数据；
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
7. 分离 case identity 与结果身份；按 run_id + case_id + case_version + repeat_index 重传幂等；
8. 核实并测试第 12 节的平台 API 映射、外部键/版本/重复提交语义（待实现验证）；
9. 同步内容执行授权、撤销检查和凭据/错误过滤。

### 完成标准

- 24 条本地 case 可完整同步；
- 同一结果重传不重复，新运行和 repeat 不覆盖旧结果；平台字段映射经目标 API 验证后方可验收；
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
- 本地 Report 和平台结果通过 run_id + case_id + case_version + repeat_index 对齐；
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

- 两个实验除独立 run_id 和被比较的 Prompt Version 外，模型、参数、工具、Context、case 版本和评估器一致；
- 可以逐 case 对比输出和评分；
- 可以查看总体指标变化；
- 发布决定有具备发布资格的实验依据，基础设施失败不可通过剔除样本获得通过结论。

## Step 12：CI 集成

### 目标

在受信任环境中自动运行 smoke 评测并可选发布 CozeLoop 实验。

### 工作内容

1. 增加 CozeLoop Secrets；
2. PR validate 不连接 CozeLoop；
3. 主分支或手工任务运行 live smoke；
4. 固定 Prompt Version，严格缓存匹配；基础设施失败保留为无效样本，报告发布不合格；
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
- 在线聊天 fallback 与固定版本 eval 禁止本地 fallback；
- CozeLoop/Eino Message 转换；
- EvalCase 字段映射；
- OpenAPI 错误分类；
- Token 和 Trace 元数据聚合；
- 凭据不进入错误或日志。

### 集成测试

- 使用 fake CozeLoop Client；
- 验证 Handler 仅注册一次；
- 验证 Prompt 固定版本；
- 验证在线聊天远程失败可回退；固定版本 eval 仅精确缓存可用，缓存不匹配或解析失败不计质量分且无发布资格；
- 验证报告 requested/resolved/source/fallback/content hash 和有效/无效计数，不得剔除失败样本宣称通过；
- 验证真实学习内容授权、撤销/重授权、授权记录及已上传不自动删除的告知；凭据永不上传、错误始终脱敏；
- 验证评测集批量同步、case/run 分离、新运行不覆盖、repeat 区分和重传幂等；平台 API 映射另需目标环境验证；
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
| Prompt Hub 不可用 | 在线聊天可回退；固定版本 eval 仅同标识/工作空间/版本缓存，否则基础设施失败且无发布资格 |
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
- 在线聊天可设置 `COZELOOP_PROMPT_ENABLED=false` 使用本地 Prompt；固定版本 eval 不得以此替代请求版本，须失败并保留无效报告。

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
