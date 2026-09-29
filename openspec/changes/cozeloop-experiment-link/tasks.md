# Tasks

> 启动门槛：任务 1.1 的目标 Workspace 契约 PoC 未通过前，不得实现或发送实验写请求。每个代码切片实施前仍需单独确认。目标 Workspace 验收未通过时，本 change 不得标为 Step 10 完成。

## 1. Workspace 契约与基线

- [ ] 1.1 使用获授权的目标 Workspace 和非敏感 sentinel 数据验证实验创建/详情/分页权限、无评测对象实际运行、Code target-output `actual_output` 与每个 LLM evaluator 输入字段映射、固定版本绑定、run-scoped pinned version 的精确 item 集/唯一 item ID，以及未知 POST 恢复语义。**验证：**至少两个 Run 各自取得精确 run-scoped immutable dataset version，并覆盖重复执行、部分写入重试；对不同字段注入互不相同 sentinel，并检查真实 score/status/固定脱敏原因码；拒绝额外/缺少/重复 item；若 Step 7–9 sync 不能提供该版本，停下并 review sync 职责，不进入任务 2。以脱敏方式记录每项通过/失败/待验证；任一关键前提失败则停下并 review spec，不进入任务 2。10–15 case 人工校准不阻塞此技术 PoC。
- [ ] 1.2 对照 Step 6–9 的实际 Workspace 验收证据、同步数据集及 evaluator 版本，冻结 Step 10 的输入/输出基线。**验证：**报告 run、run-scoped dataset/version、Workspace/API scope、远端 evaluator ID/immutable version/content hash 和每个执行身份可对照；若 dataset version 混有其他 Run 的 item 则保持 blocked；不记录凭据、真实评测内容或原始 evaluator reason。

## 2. 领域身份与快照校验

- [ ] 2.1 在 `backend/internal/eval` 定义实验领域请求、版本引用、生命周期状态和结果类型，并复用或集中执行 run/case/version/repeat 身份校验。**验证：**仅接收 live report；缺失 `CaseVersion`/repeat、跨 run、重复身份、错误版本及不同 repeat 均拒绝；含任一基础设施失败样本时整次提交在任何实验写请求前被阻断。
- [ ] 2.2 构造 report 到 run-scoped pinned dataset version 的精确 item 集核验、唯一 item ID/身份回读，以及 evaluator 固定版本输入映射。**验证：**额外/缺失/重复 item、`content_omitted` 且不能核验实际内容、仅有远端 hash、Workspace/API scope 不符、evaluator latest/tag/draft 或远端 ID/version/hash 不一致均 fail closed；report 不用于重建回答。

## 3. CozeLoop 实验 adapter

- [ ] 3.1 在 `backend/internal/infrastructure/cozeloop` 增加经 PoC 验证的实验请求/详情/结果分页 adapter，并保持 HTTP DTO 不进入 `internal/eval`。**验证：**fake HTTP 覆盖实际验证过的路由/字段、pinned-version item ID 回读、分页上限、超时、取消和错误脱敏；自由文本 evaluator reason 不进入结果持久化或日志。
- [ ] 3.2 对每个真实包含评测内容的请求增加发送前开关与授权复核。**验证：**未授权、范围不匹配、撤销发生在两次请求之间时，不发出后续内容请求；metadata-only 判定基于实际 payload。

## 4. 状态持久化与独立命令

- [ ] 4.1 实现实验侧车的最小化版本化状态、原子更新、run 路径约束和并发保护。**验证：**重启恢复、路径逃逸/符号链接、替换文件、unknown 状态、Windows ACL 和敏感字段不落盘均有测试；不修改旧 report；不持久化原始 evaluator reason。
- [ ] 4.2 增加独立实验命令，使用已有报告和已同步快照，不调用 Candidate/Judge Runner；提交需显式确认，reconcile 只查询，并通过 `--evaluator-refs` 输入远端不可变引用。**验证：**缺少显式确认、完整身份或固定远端 evaluator read-back 时在创建客户端/写请求前拒绝；未知 POST 不自动重发；命令状态不覆盖本地质量状态。
- [ ] 4.3 实现有上界、可取消的状态轮询及逐 item/evaluator 结果回关联。**验证：**部分结果、缺失/重复 item、空 score/reason、总数不符均为 incomplete，不产生质量通过。

## 5. 回归与目标 Workspace 验收

- [ ] 5.1 增加领域/fake HTTP/命令端到端测试，覆盖精确 item 集、两个 Run 隔离、重复/部分写入重试、字段 sentinel、基础设施失败阻断、未知写结果、授权撤销、reason 脱敏、重启恢复及旧报告只读兼容。**验证：**`cd backend && go test -mod=readonly ./...`、`go vet ./...`、离线命令校验、`git diff --check` 通过。
- [ ] 5.2 在目标 Workspace 完成最小实验 smoke，记录固定版本、精确 item 集、逐 item 回关联、失败语义和权限的非敏感证据，并更新 acceptance 文档。**验证：**所有 Step 10 契约有真实证据；未能测试项保持 pending，change 不得宣称平台验收完成。人工校准未完成时可验收传输/身份关联，但 LLM 分数仍仅 advisory，不可用于质量门禁。
