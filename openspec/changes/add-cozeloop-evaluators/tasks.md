# Tasks

## 1. 版本化评估器资产

- [x] 1.1 在 `backend/internal/eval` 增加评估器定义类型、清单加载、key/type/version/input schema 校验和稳定内容哈希；用 `go test ./internal/eval` 验证重复或非法清单失败、合法清单可往返解析。
- [x] 1.2 新增版本化 CozeLoop 评估器清单，以及独立的非空输出和执行状态 Code 评估器资产；用资产测试验证清单引用及 score/reason 输出字段。
- [x] 1.3 新增必含内容、禁含内容 Code 评估器资产及可选的 Tool Trace 结构评估器；用本地 case fixture 测试验证字段映射和规则语义，并将平台在线一致性记录为待验收。
- [x] 1.4 新增独立的忠实度、指令遵循 LLM 评估器提示词资产；用资产测试验证问题/材料/实际回答/参考事实映射及 0–4 分和理由输出契约。
- [x] 1.5 新增独立的完整性、可执行性 LLM 评估器提示词资产；用资产测试验证各自具有独立逻辑 key/version 且遵守相同输入输出契约。

## 2. 平台接口与 HTTP Adapter

- [x] 2.1 在 `backend/internal/eval` 增加评估器领域类型、`EvaluatorPlatform` port，以及 EvalCase/CaseResult 到 CozeLoop 输入的映射；用单元测试验证字段、基础设施失败样本排除和批量顺序。
- [x] 2.2 按固定公开 evaluator IDL 路由实现评估器 list/create/update-draft/list-versions/submit；用 fake HTTP server 测试请求响应 DTO、精确名称冲突、固定版本复用、内容哈希冲突、重试后读回和错误脱敏。
- [x] 2.3 实现远程 Validate 和 BatchDebug 的 Code/LLM 请求映射；用 fake HTTP 测试验证分数/理由解析、字段缺失和基础设施失败处理、请求限制、取消/超时传播及错误脱敏。
- [x] 2.4 对包含 case 数据或实际回答的每个 Validate/Debug 请求实施内容授权复核，同时让评估器定义管理与 case 内容上传分离；用测试覆盖开关关闭、未授权/撤销/范围不符及授权有效路径。

## 3. 显式评估器工作流与校准资产

- [x] 3.1 新增独立命令 `backend/cmd/cozeloop-evaluators`，提供本地 `validate`、远程 `debug --key --inputs <json-file>` 和 `publish --apply --key --inputs <json-file>`；publish 必须先成功 Validate + BatchDebug 才能写远端元数据，同内容固定版本复用、冲突停止。用命令测试验证本地校验无需凭据、远程动作显式触发、publish 缺少 `--apply` 时不加载配置/创建客户端，以及 API Token 不出现在错误或调试输出中。
- [ ] 3.2 新增 10–15 个 case 的人工校准记录模板，以及初始含 `workspace_verification=pending` 的 `evals/cozeloop/acceptance.md`；验证模板记录 case/version、人工/平台分数、差异、评估器版本和证据且不含凭据。
- [ ] 3.3 更新 `docs/cozeloop-platform-integration-technical-design.md` 中 Step 8–9 状态及 `evals/README.md` 相关操作说明；核对命令与 CLI 一致，并明确区分 fake/local 测试和目标 Workspace 验收。

## 4. 集成验证

- [ ] 4.1 运行 `cd backend && go test -mod=readonly ./...`、`cd backend && go vet ./...` 和 `git diff --check`；修复回归时不得回滚用户已有的未提交改动。
- [ ] 4.2 按 `specs/cozeloop-evaluators/spec.md` 逐项 review 最终实现；除非另行实测并记录证据，所有 Workspace 专属检查均保持待验证状态。
