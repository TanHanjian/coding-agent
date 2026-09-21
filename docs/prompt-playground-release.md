# Prompt Playground 与发布流程

本文定义 `interview-review-agent` 的 Prompt 调试、固定版本评测、标签发布和回滚流程。

## 1. Canonical Prompt Schema

Prompt Hub 中的 Prompt Key 固定为：

```text
interview-review-agent
```

变量必须与 `evals/interview-agent-playground.v1.json` 一致：

| 变量 | 类型 | 语义 |
|---|---|---|
| `history` | `placeholder` | 历史 user/assistant 消息，可为空数组 |
| `query` | `string` | 当前用户问题 |
| `interview_context` | `string` | 可信面试题、作答和复盘材料，可为空 |
| `conversation_summary` | `string` | 压缩后的历史会话摘要，可为空 |

工具 Schema、工具循环上限和上下文预算不放入 Prompt Hub。

## 2. Playground 场景

仓库中的场景清单将正式评测 case 分组：

- `direct-answer`：直接回答；
- `retrieval`：题库检索；
- `context-review`：题目、作答和复盘上下文；
- `missing-material`：找不到材料；
- `tool-failure`：工具失败；
- `adversarial-material`：材料内指令注入和不确定信息。

校验命令：

```powershell
cd backend
go run ./cmd/eval --mode validate-playground `
  --dataset ../evals/interview-agent.v1.jsonl `
  --playground ../evals/interview-agent-playground.v1.json
```

Playground 负责 Prompt 格式化和单轮行为对比；完整 Tool Loop、工具参数和
fixture 行为仍必须由本地 Eval Runner 验证。

## 3. 开发调试

1. 在 CozeLoop Prompt Hub 编辑草稿；
2. 使用 `development` 标签进行 Playground 调试；
3. 覆盖全部六类场景；
4. 不把 `latest` 结果用于 CI 或发布判断；
5. 调试内容采集只在获得授权并显式设置时开启。

开发运行可以按标签选择：

```powershell
$env:COZELOOP_ENABLED="true"
$env:COZELOOP_PROMPT_ENABLED="true"
cd backend
go run ./cmd/eval --mode live --prompt-label development `
  --dataset ../evals/interview-agent.v1.jsonl `
  --output ../evals/reports/prompt-development
```

## 4. 发布前固定版本评测

提交不可变 Prompt 版本后，必须使用具体版本号运行：

```powershell
cd backend
go run ./cmd/eval --mode live --require-prompt-version `
  --prompt-version 1.0.0 `
  --dataset ../evals/interview-agent.v1.jsonl `
  --output ../evals/reports/prompt-1.0.0
```

规则：

- `--prompt-version` 与 `--prompt-label` 不能同时使用；
- `--require-prompt-version` 要求 `COZELOOP_PROMPT_ENABLED=true`；
- 固定版本评测不能回退到 Label 或本地 Prompt 作为质量结果；
- 本地报告记录本次请求的 Prompt Key、Version、Label 和配置来源；该来源不替代 Trace 中的 `prompt_fallback` 实际结果；
- 评测失败、Judge 不可用或基础设施错误都必须保留报告并人工判断发布资格。

## 5. production 标签

只有固定版本评测通过并完成 badcase 人工检查后，才在 CozeLoop 控制台执行：

```text
production -> 新版本
```

代码不会自动移动平台标签。标签移动需要记录：

- Prompt Key；
- 旧 production 版本；
- 新 production 版本；
- 固定版本评测报告路径；
- 操作者和时间；
- 回滚条件。

## 6. 回滚

发现线上问题时，将 `production` 标签重新指向上一个已验证版本；必要时关闭
`COZELOOP_PROMPT_ENABLED` 使用本地 Prompt。回滚后应重新运行 smoke case，并保留
回滚前后的 Trace 和报告引用。
