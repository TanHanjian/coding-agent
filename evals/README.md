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

## 可选：CozeLoop 评估器

评估器定义和资产位于 `evals/cozeloop/`。先执行离线校验；此命令不需要 CozeLoop 凭据，也不会发起网络请求：

```powershell
cd backend
go run -mod=readonly ./cmd/cozeloop-evaluators validate
# 可选：只校验一个定义
go run -mod=readonly ./cmd/cozeloop-evaluators validate --key answer-faithfulness
```

远程 `debug` 和 `publish` 会上传评估输入及实际回答。执行前，需配置 `COZELOOP_ENABLED=true`、`COZELOOP_EVALUATION_ENABLED=true`、`COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED=true`、`COZELOOP_WORKSPACE_ID`、`COZELOOP_API_TOKEN` 和 `COZELOOP_API_BASE_URL`，并显式授予当前 Workspace 的内容上传授权：

```powershell
cd backend
go run -mod=readonly ./cmd/cozeloop-consent status
go run -mod=readonly ./cmd/cozeloop-consent grant --scopes evaluation-dataset-content
```

为单个 evaluator 准备本地 `--inputs` JSON 文件。输入必须由操作者显式提供，并包含待评估的实际 Candidate 输出；以下是 `answer-faithfulness` 的格式示例。请用获准上传的真实样例替换占位内容，不要将凭据或未经授权的用户数据写入仓库：

```json
[
  {
    "input_fields": {
      "input_query": {"content_type": "Text", "text": "<sample question>"},
      "interview_context": {"content_type": "Text", "text": "<sample context>"},
      "required_facts": {"content_type": "Text", "text": "<sample reference facts>"},
      "actual_output": {"content_type": "Text", "text": "<actual candidate output>"}
    }
  }
]
```

LLM evaluator 使用 `input_fields`。Code evaluator 则将实际回答放入 `evaluate_target_output_fields`，其余声明的字段放入 `evaluate_dataset_fields`。例如 `answer-nonempty` 的输入格式为：

```json
[
  {
    "evaluate_target_output_fields": {
      "actual_output": {"content_type": "Text", "text": "<actual candidate output>"}
    }
  }
]
```

从 `backend/` 执行远程调试或显式发布；将 `<local-inputs.json>` 替换为对应 evaluator 的本地输入文件路径：

```powershell
go run -mod=readonly ./cmd/cozeloop-evaluators debug --key answer-faithfulness --inputs <local-inputs.json>
go run -mod=readonly ./cmd/cozeloop-evaluators publish --apply --key answer-faithfulness --inputs <local-inputs.json>

# Code evaluator 示例：输入文件须按上方 evaluate_target_output_fields 格式准备
go run -mod=readonly ./cmd/cozeloop-evaluators debug --key answer-nonempty --inputs <local-inputs.json>
go run -mod=readonly ./cmd/cozeloop-evaluators publish --apply --key answer-nonempty --inputs <local-inputs.json>
```

`debug` 会先执行远程 Validate，再执行 BatchDebug。`publish` 必须提供 `--apply`，并在任何远程元数据变更前通过这两项检查；它只复用内容哈希匹配的固定版本，遇到版本冲突会停止。不要从持久化评测报告重建实际回答，也不要为了生成 evaluator 输入而重新运行 Agent。远程操作前会检查功能开关与当前内容授权；`COZELOOP_CAPTURE_CONTENT` 不是评测内容授权的替代品。评估器发布、fake-server 测试及本地校验均不等同于目标 Workspace 验收；请在 `evals/cozeloop/acceptance.md` 留存不含凭据的 Workspace 证据，未实测时保持 `workspace_verification=pending`。

## 可选：同步到 CozeLoop

同步会上传 case 输入、fixture/期望、Agent 输出、脱敏工具轨迹、评分、Usage 和 Trace ID。它默认关闭，且必须同时开启配置并明确授予本地内容授权。先在 `backend/.env` 配置 `COZELOOP_ENABLED=true`、`COZELOOP_EVALUATION_ENABLED=true`、`COZELOOP_EVALUATION_CONTENT_UPLOAD_ENABLED=true`、Workspace ID、API Token 和 API Base URL；不要把 Token 写入报告或提交到仓库。`COZELOOP_CAPTURE_CONTENT` 是 Trace 内容采集的独立开关，不会替代评测内容授权。

```powershell
cd backend
# 查看当前授权状态；首次授权会显示数据范围并要求交互确认
go run ./cmd/cozeloop-consent status
go run ./cmd/cozeloop-consent grant --scopes evaluation-dataset-content

# 只有明确授权后才运行此命令上传评测内容
go run ./cmd/eval --mode live --publish-cozeloop --dataset ../evals/interview-agent.v1.jsonl --output ../evals/reports
```

同步报告保存在 `evals/reports/<run_id>/`。若远端失败，授权有效时本地会在受限目录暂存同一 run 的 payload；修正权限或网络后，用报告目录名作为 `run_id` 续传：

```powershell
go run ./cmd/eval --mode sync-pending --run-id <run_id> --output ../evals/reports
```

正常 UUID run 的评测集版本按 CozeLoop 的 SemVer 约定命名为 `0.0.0+run.<run_id>`；不能直接编码的旧 run ID 使用确定性摘要作版本标识，完整身份仍保留在 payload 和数据项中。数据项仍以 `run_id + case_id + case_version + repeat_index` 标识。重复续传复用同一身份，身份对应内容发生变化时会拒绝覆盖。撤销时运行 `go run ./cmd/cozeloop-consent revoke` 并在提示符确认；这会停止后续本地授权采集/上传并清除本地待同步内容，但不会删除已上传到 CozeLoop 的数据。如果 pending 目录被替换成符号链接，命令会移除链接并返回清理错误，不会跟随删除未知路径；需按报错手工检查目标位置。

本地 Go tests 和 fake-server 测试只验证实现与模拟契约。真实字段展示、Workspace 权限、重复提交和部分失败续传必须在目标 Workspace 验证；当前未配置该 Workspace 凭据，因此不能据此宣称平台端验收完成。
