# Design

## Evaluation flow

```text
JSONL case
  -> fixture question service
  -> production interview.Builder + Eino Executor
  -> in-memory TextSink + tool recorder
  -> deterministic scorer (60 points)
  -> optional independent Judge (40 points)
  -> report.json + summary.md
```

## Identity and version contract

`case_id` 标识评测样例，`case_version` 标识样例内容版本；`run_id` 标识一次评测运行，`execution_id` 标识该 run 中一次实际执行。每个执行记录必须同时带有 `run_id`、`case_id`、`case_version` 和 `repeat_index`，不得用一个 id 兼任这些语义。执行身份固定为 `run_id + case_id + case_version + repeat_index`。同一次运行的网络重传或上传重试 MUST 复用原 run/execution 身份；只有重新开展独立评测才生成新 `run_id`。配置只作为元数据，不改变身份语义。

固定 Prompt 版本评测 MUST 指定具体 `prompt_version`，它与数据集的 `case_version` 是不同概念。仅接受该 Prompt 版本或匹配工作空间、Prompt key、具体版本的缓存，缓存内容必须通过完整性检查；远程获取、格式化或转换失败且无法使用该版本时，记为基础设施失败，禁止回退本地 Prompt。不计该执行的质量分，报告保留无效样本且整轮不具备发布资格，不能剔除失败样本后判定发布通过。

仅在线聊天允许按可用性策略回退本地 Prompt；live eval 仍遵守上述严格版本策略。所有评测报告必须记录 `requested_version`、`resolved_version`、`source`、`fallback` 和 `content_hash`，并记录有效/无效样本数量、基础设施失败数量及发布资格结论。平台字段映射在验证前只作为待验证扩展，不作为本地 MVP 必需依赖。

### 验收场景（待实施）

- 请求 Prompt vA 且远程不可用时，仅同工作空间、Prompt key、vA 的有效缓存可继续；其他版本缓存或本地 fallback 不得作为 vA 计分。
- 部分 case 的 Prompt 解析失败时，报告保留 requested/resolved/source/fallback/content_hash（解析失败可为空）、有效/无效计数，并明确不具备发布资格。
- 同一 case repeat 三次生成三个独立执行结果；上传重试复用身份，不增加样本；相同配置重新评测使用新 run_id，不覆盖旧运行。
- 平台数据项和实验字段的身份映射经接口语义验证后才能实现，不预设平台原生支持这些外部键。

## Score contract

总分 100：工具选择 15、工具参数 15、工具顺序/次数 10、必须事实 12、禁止内容 8；Judge 的忠实度 15、指令遵循 10、完整性 10、可执行性 5。Judge 的每项原始分数为 0–4，按权重换算。关键工具错误、编造事实、泄密或禁止调用直接失败；Judge 不可用时状态为 `unscored`。

## Data and privacy

评测运行默认本地完成，不发送用户真实学习内容，不上传凭据；任何可选 Trace 仍须遵循独立授权规则，不能用评测配置替代用户授权。普通日志、诊断和 UI 摘要只保留脱敏信息。

评测数据存放在 `evals/interview-agent.v1.jsonl`，每条 case 自包含题目、作答和复盘 fixture。runner 不初始化 SQLite、不调用 HTTP、不持久化工具轨迹；报告只保留脱敏后的工具名、结构化断言结果和 Judge 理由。

## Runtime configuration

Candidate 使用现有 `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`。Judge 必须使用独立的 `EVAL_JUDGE_API_KEY`、`EVAL_JUDGE_BASE_URL`、`EVAL_JUDGE_MODEL`。`validate` 模式不需要任何模型配置。
