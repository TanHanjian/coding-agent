# Tasks

> 启动门槛：Step 10 已通过目标 Workspace 验收；人工 A/B 阈值/样本数未确认前只输出差异，不生成胜者或发布资格结论。每个代码切片实施前仍需单独确认。

## 1. A/B 输入与可比性契约

- [ ] 1.1 定义两个 run/report/experiment 引用及由实际运行时生成、绑定 `run_id` 与 report digest 的不可变执行清单。清单覆盖实际 Prompt 解析引用、模型 provider/endpoint 身份指纹与生效参数、工具版本/内容摘要、Context/fixture 内容摘要、各阶段 timeout、case/version/repeat 身份、Judge/evaluator 不可变版本。**验证：**清单不可由 A/B 调用方手工声明或覆盖；缺失字段、来源不可信、digest/run 绑定不符均有明确失败类别。
- [ ] 1.2 实现逐字段可比性校验，主 Agent Prompt 版本作为唯一允许差异；检查 Workspace、fallback、控制变量和完整身份集合。**验证：**逐 row `run_id`、显式 case version/repeat 必须与各自清单一致；共同缺失、重复或控制变量漂移及严格 Prompt 解析失败均标记不可比。

## 2. 逐 case 比较与指标口径

- [ ] 2.1 分别将两侧 report 与各自完整预期身份清单核对，再按 case/version/repeat 构造配对，保留左右 run 身份及缺失、重复、无效、基础设施失败、未评分和平台 incomplete 项。**验证：**跨 run/repeat 不串配；共同缺失、row identity 缺失/不符或基础设施失败时整体不可比，不得使用剩余交集输出无条件比较结论。
- [ ] 2.2 输出本地硬失败、deterministic/Judge/CozeLoop 分数、Candidate/Judge Usage 与准确的时延口径。**验证：**缺失 Usage 显示 unavailable；含 Judge 的 DurationMS 不标作 Candidate latency；无价格表时不输出货币成本。

## 3. 报告、隐私与人工决策

- [ ] 3.1 生成机器可读比较结果和白名单摘要，保留显式分母及不可比原因。**验证：**以敏感字段 sentinel 测试回答、Prompt、工具参数、Judge 理由、CozeLoop 自由文本 evaluator reason 和原始错误不会进入比较产物或可分享摘要；仅允许白名单脱敏原因码。
- [ ] 3.2 增加人工决策记录入口/格式，关联经核验的 reviewer 身份、时间、左右 run/experiment/Prompt 版本、脱敏理由和受控证据引用；不执行标签写操作。**验证：**决定记录可复核且不含原始 evaluator reason/评测内容，工具没有自动发布或 production 标签副作用。

## 4. 测试与 Workspace 验收

- [ ] 4.1 增加运行清单生成/绑定、report identity 完整性、结果投影、reason 脱敏及命令测试。**验证：**清单必须来自实际运行时；篡改 digest、run/row identity 缺失/不符、共享样本缺失或敏感 reason sentinel 均失败；`cd backend && go test -mod=readonly ./...`、`go vet ./...`、离线 compare/validate 与 `git diff --check` 通过。
- [ ] 4.2 使用目标 Workspace 两个固定 Prompt run 和各自已验收实验结果完成 A/B smoke，记录逐 case/repeat 关联、控制变量和非敏感证据。**验证：**真实 Workspace 结果全部可回关联；A/B reviewer 对照记录存在；缺少阈值时仍不生成胜者结论。
- [ ] 4.3 更新 A/B 操作说明和 acceptance 状态。**验证：**文档明确本地/平台验收差异、样本分母、隐私和人工发布边界；未完成项保持 pending。
