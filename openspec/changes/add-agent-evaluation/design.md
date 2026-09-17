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

## Score contract

总分 100：工具选择 15、工具参数 15、工具顺序/次数 10、必须事实 12、禁止内容 8；Judge 的忠实度 15、指令遵循 10、完整性 10、可执行性 5。Judge 的每项原始分数为 0–4，按权重换算。关键工具错误、编造事实、泄密或禁止调用直接失败；Judge 不可用时状态为 `unscored`。

## Data and privacy

评测数据存放在 `evals/interview-agent.v1.jsonl`，每条 case 自包含题目、作答和复盘 fixture。runner 不初始化 SQLite、不调用 HTTP、不持久化工具轨迹；报告只保留脱敏后的工具名、结构化断言结果和 Judge 理由。

## Runtime configuration

Candidate 使用现有 `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`。Judge 必须使用独立的 `EVAL_JUDGE_API_KEY`、`EVAL_JUDGE_BASE_URL`、`EVAL_JUDGE_MODEL`。`validate` 模式不需要任何模型配置。
