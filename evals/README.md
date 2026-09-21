# 面试 Agent 评测集

数据集是版本化 JSONL，每行一个独立 case。评测不读取 SQLite；live runner 会把行内的题目、作答和复盘数据注入 fixture service。

## 校验数据

```powershell
cd backend
go run ./cmd/eval --mode validate --dataset ../evals/interview-agent.v1.jsonl
go run ./cmd/eval --mode validate-playground --dataset ../evals/interview-agent.v1.jsonl --playground ../evals/interview-agent-playground.v1.json
```

`validate-playground` 会校验 Prompt 变量 Schema，并确保每个正式评测 case
恰好映射到一个 Playground 场景。

## 本地运行

Candidate 使用 `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`。要启用 40 分的 LLM Judge，另外设置 `EVAL_JUDGE_API_KEY`、`EVAL_JUDGE_BASE_URL`、`EVAL_JUDGE_MODEL`。

```powershell
cd backend
go run ./cmd/eval --mode live --dataset ../evals/interview-agent.v1.jsonl --output ../evals/reports
go run ./cmd/eval --mode live --tag smoke --dataset ../evals/interview-agent.v1.jsonl --output ../evals/reports

# 固定 Prompt 版本运行，适用于发布前评测
$env:COZELOOP_ENABLED="true"
$env:COZELOOP_PROMPT_ENABLED="true"
go run ./cmd/eval --mode live --require-prompt-version --prompt-version 1.0.0 --dataset ../evals/interview-agent.v1.jsonl --output ../evals/reports/prompt-1.0.0
```

`--prompt-version` 与 `--prompt-label` 不能同时使用。开发调试可以使用
`--prompt-label development`；发布前和 CI 必须使用 `--require-prompt-version`
加具体版本号。

总分 100：工具行为 40 分，答案硬断言 20 分，Judge 质量评分 40 分。`>=80` 为通过；硬断言失败直接失败；Judge 不可用时为 `unscored`。
