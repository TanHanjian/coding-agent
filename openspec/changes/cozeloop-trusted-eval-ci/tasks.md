# Tasks

> 启动门槛：普通 PR 离线验证不得等待 CI 授权。现有未受保护的 live smoke 与 `evals/reports/` 全目录上传必须在 Step 12 实施时关闭。启用模型 endpoint 请求、CozeLoop 内容上传和任何 CI artifact 上传，须分别等待对应授权/治理批准及可信触发器 review；不得由自动化生成 consent。每个代码切片实施前仍需单独确认。

## 1. CI 状态与安全产物契约

- [ ] 1.1 定义 local execution、quality、`model_request_status`、`platform_status`、`artifact_status` 和 eligibility 的机器可读状态、缺密钥/授权的 disabled/skipped 语义及退出映射。**验证：**本地质量失败、remote 阶段失败或跳过均不会互相覆盖，skipped 不代表质量通过，平台评分 advisory。
- [ ] 1.2 明确安全摘要白名单、artifact 访问者和保留期；未批准的字段/政策保持排除。**验证：**由安全/产品责任人 review 字段清单；artifact governance 未批准时不上传任何 artifact（包括白名单摘要），也不上传原始报告或私有链接。
- [ ] 1.3 分别确认模型 endpoint 请求授权、CozeLoop 内容上传授权、可信触发器/受保护 Environment 审批人及撤销机制。**验证：**每种授权均有独立批准和 Workspace/API scope；没有批准时标记 blocked，任务 3 中对应分支保持禁用/跳过。

## 2. 本地结果与隐私投影

- [ ] 2.1 增加白名单安全摘要 projector 和机器状态输出，不直接序列化完整 report。**验证：**输入含回答、Prompt、Tool arguments、Judge rationale、CozeLoop 原始 evaluator reason、fixture、Token/原始错误 sentinel 时，输出不含敏感值。
- [ ] 2.2 增加状态/退出码回归测试，保持本地 Scorer/Judge、hard failure 和发布资格权威。**验证：**所有质量/平台成功失败组合均有测试，平台状态不改变本地结果。

## 3. Workflow 分流与安全验证

- [ ] 3.1 保持普通 PR/fork 的离线 Go、manifest 和安全投影检查，确保不读取 CozeLoop/model Secrets。**验证：**PR 任务无平台凭据仍通过；workflow 不使用不可信代码读取 Secrets 的 privileged PR 模式。
- [ ] 3.2 先关闭当前无 protected ref/environment 约束的 live smoke；仅在模型 endpoint 请求授权和可信触发器均批准后，配置受保护 live job、固定 Prompt/evaluator 版本、`--require-prompt-version`、显式状态输出及无授权跳过路径。**验证：**未审批/非可信 ref 时不启动 live 调用；缺密钥/授权时无内容请求；任何 fallback 或 Prompt 版本不符均不可 eligible。
- [ ] 3.3 先移除/禁用 `evals/reports/` 全目录 artifact 上传；仅在 artifact 访问/保留政策批准后启用独立白名单摘要路径。**验证：**审批前 CI 完全不上传 artifact；审批后静态检查和敏感 sentinel 检查通过，且永不上传整个 `evals/reports/` 目录。

## 4. 端到端验收与文档

- [ ] 4.1 验证无 Secret PR、live 模型请求未授权、CozeLoop 上传未授权、artifact governance 未批准、受信任授权任务、平台失败、本地质量失败和 artifact 访问边界。**验证：**CI 结果逐类对应验收矩阵；审批前模型/live、平台请求与 artifact 上传均 skipped/blocked；skipped 不得映射为 quality pass，失败不得被 `continue-on-error` 或 shell exit 0 隐藏。
- [ ] 4.2 仅在模型请求授权、CozeLoop 内容上传授权、可信 runner 和 artifact access/retention policy 分别批准后，完成固定 Prompt/evaluator 版本的最小 Workspace 任务及 artifact 人工 review。**验证：**有非敏感 Workspace 结果摘要和 artifact 隐私审查记录；任一授权未批准/不可用时对应分支保持 disabled/skipped，Step 12 不得标完成。
- [ ] 4.3 更新 `evals/README.md`、workflow 操作说明和 `acceptance.md`。**验证：**文档列明触发方式、授权/撤销、skip/失败分类、artifact 访问与保留；不含凭据。
