# CozeLoop 全链路代码优化方案（Step 1–12 接入完成后）

| 项目 | 内容 |
| --- | --- |
| 状态 | Draft，供设计 review；**不是代码修改授权** |
| 适用时点 | Step 1–12 完整接入且有目标 Workspace 验收证据后，开展整体结构优化；接入期间发现的安全/正确性阻断项应提前修复 |
| 范围 | 本项目所有 CozeLoop 接入代码：配置、Client/Trace、Prompt、授权、评测执行、数据集、评估器、实验、A/B、CLI、报告与 CI；不重构无关的 Agent 业务逻辑 |
| 依据 | [Step 10–12 技术设计](cozeloop-step10-12-technical-design.md)、[Step 1–12 总体方案](cozeloop-platform-integration-technical-design.md)、[闭环验收计划](cozeloop-completion-implementation.md)、当前仓库只读静态审查 |
| 重要说明 | 文中“当前”是静态审查发现的事实；“接入完成后的目标结构”是拟议方案，不能误写为已经实现。新增 Step 10–12 后须再次审查实际代码，不能机械按本文假设重构。 |

## 1. 目标、验收前提与两类工作

本方案**不是** Step 10–12 的功能实现待办清单，也不是“把已有阻断错误修一遍”。它回答的是：当 Prompt Hub/Trace/评测集/评估器/实验/A/B/CI 全部接入、功能和 Workspace 行为已经固定后，如何使整个 CozeLoop 实现有清晰模块边界、较少重复、可测试的状态转换、一致的数据与隐私契约以及更低的长期修改成本。

**优化前提：**Step 1–12 能在目标 Workspace 以真实权限完成验收；每个阶段有固定的 Prompt/数据集/评估器/实验版本和受控证据；本地 Runner 的硬失败与发布资格仍为权威；现有回归测试、旧报告和 pending 格式可作为兼容基线。若 Workspace 不支持无评测对象实验，先调整/评审接入设计，不把未完成的实验代码当作优化基线。

**区分两条轨道：**

| 轨道 | 时点及目的 | 范围举例 |
| --- | --- | --- |
| A. 接入正确性阻断项 | 完成相应 Step 的验收前修复，不能以“等整体优化”延期 | 真实内容泄露风险、跨 run 的数据集污染、内容被省略却宣称 hash 已核验、失联 POST 后盲重试、Workspace 字段误配；详见 Step 10–12 设计 §2/§4 |
| B. 全局结构优化 | 全链路功能稳定并验收后，按可回滚的独立切片重构 | 共享装配/生命周期、清晰的领域用例、数据目录单一来源、状态与错误类型化、报告分级、跨适配器安全能力收敛、可测试性和性能基线 |

A 中某些变更本身会影响 B 的接口；整体优化必须以 **A 已修复的实时代码** 为准，不能为了重构再次退回不安全行为。验收前的受控安全产物要求不是“优化可选项”。

**预期成果（非文件数目标）：**改动一个授权规则不必逐个寻找数据集/评估器/实验 HTTP 调用；改动数据集字段不必在 schema、数据行和 Debug 输入重复人工同步；创建一次 live run 不必同时理解 CLI、pending 文件、报告和远端状态的写入顺序；测试可从少量明确的接口观察端到端结果，无需操纵不可重置的全局变量；旧版本数据和未启用 CozeLoop 的本地路径不退化。

## 2. 全链路审查清单：当前证据与完成后的目标

以下表格故意覆盖全部 Step，而非只列 Step 10–12 的问题。每行的“目标”是**待接入完成后复核的设计方向**，不暗示该问题已经造成线上故障。

| Step / 区域 | 当前代码可观察的事实（路径） | 优化目标与不应改变的行为 |
| --- | --- | --- |
| 1 基础配置/Client | `backend/internal/infrastructure/config/config.go` 的 `CozeLoopConfig` 同时承载 SDK、Trace、Prompt、Evaluation 和授权开关；`infrastructure/cozeloop/client.go` 初始化 SDK/Trace 授权，`evaluation_client.go` 另起 HTTP Client 并校验上传授权 | 将共享 endpoint/凭据校验与 Tracing、Prompt、Evaluation 各自的能力配置和生命周期区分，集中解析优先级/默认关闭语义；不建需要所有能力同时启用的万能 Client |
| 2 Trace | `infrastructure/cozeloop/tracing.go` 的进程级 `registerGlobalOnce` 首次绑定 Handler；客户端 `Close` 自身 `sync.Once`，但重复注册不同客户端的冲突没有显式诊断；当前追踪实现依赖 `eval.TraceCapture` | 明确进程级所有权、关闭和配置冲突规则，解除通用追踪协议对评测包的反向依赖；确保流式 Eino 回调、Usage、Candidate/Judge Trace 不串线；不通过重注册全局 Handler 掩盖错误 |
| 3–4 PromptProvider | `agent/prompt/provider.go`、`infrastructure/cozeloop/prompt_provider.go`、`agent/interview/builder.go` 与 `eval/run_observability.go` 分担版本选择、格式化/模板化、hash 与审计；在线 fallback 与固定版本 strict 语义已形成 | 在 Prompt 模块建立“请求选择 → 实际解析 → 模板/渲染 hash → 来源/回退”统一契约；区分模板 hash 和一次运行内容 hash 的含义，避免同名字段被不同层计算。不能让固定版本评测退回本地 Prompt；取消/超时必须保持传播 |
| 5 Playground/发布 | Playground 清单和发布操作在 `evals/`、`docs/prompt-playground-release.md`，版本切换仍经人工；未由 CLI 自动化生产标签 | 对发布依据、Prompt/评估器/实验版本与本地报告建立统一的**审计引用结构**，但不把平台发布操作塞入业务运行时，也不自动切 production 标签 |
| 6 执行与可观测性 | `eval/run_observability.go` 用 TraceCapture 管理 Candidate/Judge 元数据，`infrastructure/cozeloop/tracing.go` 读取评测上下文；报告有 Candidate/Judge Trace 和 Usage | 将通用关联上下文放在中立观测 seam，评测包只描述 run/case/repeat 语义；Usage 未返回保持 unavailable，不写 0；Trace 未启用不影响本地评测 |
| 7 数据集 | `eval/dataset_sync.go` 的字段声明与 `datasetFields` 写值分离，`BuildDatasetItems` 用 case ID 建映射；`infrastructure/cozeloop/evaluation_client.go` 管读写与 hash 校验；`cmd/eval/main.go` 管 pending/report 状态和错误字符串分类 | 字段目录/净化规则单一来源；身份和快照证明明确；把同步/续传状态机从 CLI 移至窄用例，减少平台 DTO 往返重复；已有 hash、同 run 重试、legacy pending 迁移不得退化 |
| 8–9 评估器 | `eval/evaluator.go` 通过 `BuildDatasetItems` 再构造 Debug 输入；`infrastructure/cozeloop/evaluator_client.go` 做字段规范化/序列化；`cmd/cozeloop-evaluators/main.go` 与 eval 层共同承担发布编排 | 领域层做声明字段的最小化投影、基础设施层做平台 DTO 映射；一个可测试的发布用例保证 Validate/BatchDebug → 元数据写入 → 固定版本读回；保留 manifest hash 冲突停止和人工校准 |
| 10 实验（未实现） | 截至本方案审查，`EvaluationPlatform` 仅含数据集能力，无实验 port/adapter/命令 | 接入完成后，复核实验状态、读回与本地报告是否使用同一执行身份及错误契约；不要把实验塞进数据集 Client 或重新运行 Agent；结果拉取必须可靠回关联 |
| 11 A/B（未实现） | 当前没有双 run 可比性模型；`CaseResult.DurationMS` 包含 Judge，`run_id` 输出目录只在发布时隔离 | A/B 用例只接收两个已完成且各自有版本指纹的运行；配对逻辑和对比报告在领域层，不依赖 CozeLoop 页面顺序；Candidate/Judge 时间和成本口径清楚，坏例与无效计数保留 |
| 12 CI（部分已有） | `.github/workflows/evaluation.yml` 离线 PR + advisory smoke，`continue-on-error` 与命令内 warning 可能模糊质量/平台状态，artifact 上传整个报告目录 | CI 消费稳定的机器可读状态和**白名单脱敏摘要**；离线 PR 与有授权的可信作业分离；Skip、质量失败、平台失败不同，不因改造把 CozeLoop 变成普通 PR 的必要依赖 |
| 横切授权与文件 | `infrastructure/cozeloop/consent.go` 同时包含授权记录、撤销和数据集 pending 文件；HTTP adapter 对内容/元数据请求分类；`cmd/eval/main.go` 用错误文本判断撤销 | 授权策略、私有文件存储、平台通信分别聚焦职责；根据**实际载荷**作内容判定，类型化错误驱动撤销与恢复，保留当前逐请求校验、Workspace/API 地址绑定和 fail-closed 语义 |
| 横切报告 | `eval/model.go` 的 `Answer` 不持久化，但 `ToolTrace.Arguments`、Judge 理由、HardCheck reason、Error 可序列化；`eval/report.go` 生成 JSON/Markdown | 受限原件、暂存待上传载荷、面向 CI/分享的白名单报告互不混用；同时保持真实内容只在有权限的评测/远端数据集中可见，不因安全重构丢失已授权的正常能力 |

### 2.1 优先保留的深模块

已有 `agent/prompt.Provider` 是真实选择点（本地与远程/strict）；本地 Scorer 和 Judge 是独立权威；`eval.EvaluationPlatform`、`eval.EvaluatorPlatform` 把平台 DTO 挡在基础设施层。**不因“统一架构”而合并成覆盖所有 CozeLoop 功能的巨大接口。** 重构的方向是减轻调用方必须知道的顺序和细节，而不是给每个函数再包一层透传 wrapper。

## 3. 拟议目标结构与依赖方向

不预设必须搬迁到某个新包；先定义模块责任和接口，再在 OpenSpec/代码 review 时定最终路径。原则：领域层依赖窄的可替换 port；CozeLoop SDK/HTTP DTO 只在基础设施适配器；命令负责参数/装配/退出状态；数据保护在外传字段投影和发送边界双重防护。

```text
cmd/server, cmd/eval, cmd/cozeloop-evaluators, 后续实验/A-B 命令
    └─ composition / process lifecycle（一次装配、一次关闭、显式开关）
          ├─ agent/prompt.Provider → local/strict/remote adapter
          ├─ observability context → Eino Trace adapter
          └─ eval use cases
                ├─ RunEvaluation（本地执行/本地质量）
                ├─ SyncRunDataset（内容投影、快照核验、pending 恢复）
                ├─ PublishEvaluator（验证、调试、发布、读回）
                ├─ SubmitOrReconcileExperiment（固定版本、状态和 item 映射）
                └─ CompareRuns（只读、严格可比性和审计）
                      ↓ 各自所需的窄 port
              infrastructure/cozeloop：平台 SDK、HTTP transport、DTO
              private storage / consent：授权与受限文件的独立实现
```

**Seam 设计判据：**

1. 只有业务策略会变化、且测试需要 fake/本地替代的依赖，才保留对外 port；仅一个实现的纯校验/序列化函数不再增设抽象。
2. `RunEvaluation` 输出不可变的 run 身份与评测结果；`SyncRunDataset` 接收经校验的运行数据及明确授权，不再从 CLI 隐式寻找同名文件；实验只消费已核验快照，A/B 只消费已完成运行引用。
3. 端点、错误脱敏、同源重定向、分页和授权重查属于平台适配的通用安全实现；“本次允许外传哪些领域字段”属于调用方的明确数据契约。避免在两层分别默默补全敏感字段。
4. Prompt、Trace 与 Evaluation 可独立开关与关闭；共享配置解析不等于共享同一个必须全开的 Client。授权记录与 pending 存储可以共享受限目录的基础操作，但不把两种业务生命周期绑死。
5. 一个窄用例应隐藏复杂状态转换（提交、读回、续查、恢复），而非暴露 `Create/List/Update/...` 十几个方法让所有命令重新拼流程；保留细粒度 HTTP port 供用例内部测试，不把内部步骤变成外部调用约束。

**备选方案与取舍：**保持所有逻辑在 `cmd` 最少搬迁但 CLI 会继续承载异常恢复；把所有能力统一到 `CozeLoopManager` 可以减少文件却制造巨型 interface 与配置耦合；大规模先迁包再补测试风险过高。优先选“小切片提炼用例 + 保留现有领域端口 + 稳定后再重排文件”。

## 4. 模块优化详案与不变量

### 4.1 配置、装配与追踪生命周期

- **现状问题：**server 和 eval 命令分别建立 Client、注册全局 Handler、选择 Prompt Provider、关闭；不同 Client 使用 `registerGlobalOnce` 时会返回成功却继续复用先前 Handler。见 `backend/cmd/server/main.go`、`backend/cmd/eval/main.go`、`infrastructure/cozeloop/tracing.go`。
- **方案：**将“请求启用哪些能力—检查配置/授权—构建依赖—登记全局追踪所有权—返回 Close”收为一个可注入装配入口，server HTTP 构建仍归 server。进程中重复注册须区分“同配置重复装配”和“与首次注册冲突”；不可假定 `sync.Once` 能支持替换。测试中通过局部 callback/显式装配测试 seam，避免靠包级不可重置变量的顺序获得通过。
- **不变量：**默认关闭不需要凭据；启用时 Token 不进入错误；全局 Handler 只注册一次；关停后 flush/close 不影响 Agent 结果；不凭远程失败改变聊天业务错误；Candidate/Judge Trace 分离。
- **时机：**先拿到 Step 1–12 生命周期回归基线，改动 server 和 eval 两个入口即可证明收敛有价值；不要提前为所有命令设计抽象框架。

### 4.2 Prompt 与审计模型

- **现状问题：**CLI 与 provider 多处决定 Version/Label 优先级；Provider 格式化后转换 Eino 模板，Builder 再渲染；本地/远端/评测 wrapper 分别贡献 hash 和解析元数据。见 `agent/prompt/provider.go`、`infrastructure/cozeloop/prompt_provider.go`、`agent/interview/builder.go`、`eval/run_observability.go`。
- **方案：**明确 `RequestedSelection`、`ResolvedSelection` 与 `RenderedForRun` 的概念。固定版本的 Workspace/Key/Version 与内容 hash 在一次 run 内可核验；若既要模板内容 hash 又要含用户变量的渲染消息 hash，使用**两个命名不同**的字段与权限规则，不能复用同一个 `prompt_content_hash` 含混表达。在线 fallback 和 strict eval 仍是不同策略，统一的是返回契约和审计，而非强制相同行为。
- **不变量：**Version 优先于 Label；真实取消/超时不回退；strict eval 不静默改用本地或其他 Workspace 缓存；Tool Schema 和安全规则仍留 Go；PromptFormat 中 history 等变量符合平台类型；报告不得包含原始 Prompt/用户内容。
- **性能判断：**基于真实工作负载测 GetPrompt、PromptFormat、Graph 构建、hash 与授权检查开销；只有测出瓶颈且缓存键含 Workspace/Key/Version/内容摘要、撤销后不继续上传，才引入缓存或复用编译结果。

### 4.3 运行身份、用例状态与安全报告

- **现状问题：**`BuildReport` 从首条结果派生 RunID、`BuildDatasetItems` 按 case ID 找定义并允许结果 `CaseVersion==0`；同步的 pending、报告中的 `CozeLoopSync`、远端版本分别持有状态，CLI 管读写顺序；live 不发布时报告路径可能被下个 run 覆盖。见 `eval/report.go`、`eval/dataset_sync.go`、`cmd/eval/main.go`。
- **方案：**建立一个唯一校验 `run_id + case_id + case_version + repeat_index` 的执行身份构造点。将“本地执行质量”“同步状态”“实验状态”“A/B 决策”分层，不共用 `failed` 一个模糊字符串。恢复入口接受 run 引用而非靠目录名猜测，持久写入有原子更新/崩溃恢复规则；所有 live run 自然隔离并兼容旧报告路径。报告分受限原件和白名单摘要，CI 不上传原件；pending 成功清理、失败可续传、撤销清除的语义保留。
- **不变量：**本地 hard failure 与无效样本不会因远端成功变成合格；`Answer` 不进入公开报告，Tool 参数/错误/Judge 理由也不得经 summary、artifact 旁路泄露；缺失 Usage 不作为 0；旧报告/旧 pending 可以安全只读迁移，无法验证时停止而非重放。

### 4.4 数据集、评估器、实验的字段目录与平台传输

- **现状问题：**字段在数据集 schema、写入 map、评估器清单、Debug input 与平台 payload 多处表达；`BuildEvaluatorInputBatch` 通过 `BuildDatasetItems` 复用字段，随后 adapter 再规范化；每增加字段需理解每层解释。见 `eval/dataset_sync.go`、`eval/evaluator.go`、`infrastructure/cozeloop/evaluator_client.go`。
- **方案：**建立版本化的**外传字段目录**（名称/类型/来源/可选性/敏感等级/目标消费能力），由同一个纯投影生成数据集 fields 和声明过的 evaluator 输入；adapter 只负责 CozeLoop wire DTO，绝不隐式从整行添加未声明字段。实验字段映射只引用读回验证过的目录版本、数据集 item ID 和 evaluator 版本。删除冗余中间 map 之前通过契约测试验证每个旧字段语义。
- **不变量：**Tool Trace 的 arguments 值不上传；Judge rationale、fixtures、参考事实等是否允许外传仍服从内容授权；元数据管理接口携带代码/Prompt 资产时不等同“无内容”；真正敏感的请求每次发送前检查授权。服务端 `content_omitted` 必须体现为不可核验，不能仅信远端自报 hash。
- **拒绝方案：**不把所有 schema 绑定到 CozeLoop 原生 DTO；不靠 Go 反射自动上传 `CaseResult`/`EvalCase` 全字段；不为了复用而允许所有评估器看见所有数据。

### 4.5 错误、并发和资源发布

- **现状问题：**授权撤销通过错误文本匹配决定 pending 删除；资源创建/提交版本存在响应丢失与共享 draft 的并发风险；未来实验 POST 同样可能在本地不知成功与否。见 `cmd/eval/main.go`、`eval/evaluator.go`。
- **方案：**定义领域安全错误类别（配置、无授权/撤销、内容不可核验、身份冲突、凭据/权限、网络、未知写结果、评估结果不完整）；原始 HTTP body/Token 不进入业务错误。每个写用例定义“可重试、只读恢复、必须人工核对”分支；同一 evaluator 版本发布和同一 run 实验提交串行化，但不将本地锁当跨机器 exactly-once 证明。
- **不变量：**固定版本同内容可复用、异内容停止；Validate/BatchDebug 成功后才允许显式发布；平台不可用不清除本地报告；撤销后不继续发送 pending 内容；分页/轮询有上限且可取消；未知实验写结果不得盲重发。

### 4.6 CLI、CI 与可测试性

- **方案：**各 CLI 薄化为“解析参数 → 构建用例依赖 → 执行 → 映射为可机器读取的安全状态/退出码”，不重复状态流程；领域测试用 fake port，基础设施测试用 fake HTTP，装配测试覆盖 server/eval 实际开关组合，再以真实 Workspace 做少量关键契约验收。CI 普通 PR 离线，可信任务固定版本、已批准授权、分级产物。
- **不变量：**没有 Workspace/凭据仍能本地执行和离线测试；有 API Token 但无内容授权不能上传；本地质量失败、平台失败、无授权跳过具有不同状态；未经校准平台分数不成为硬门禁。

## 5. 按风险和依赖实施的重构切片

每片在实际实施前须单独 review 行为不变边界及影响面，**不把整张表一次性作为改代码授权**。功能改变（如默认路径或新增错误状态）单独写 OpenSpec 与迁移说明，不能偷放在“纯重构”里。

| 阶段 | 切片与主要受影响区域 | 明确收益 | 前置/兼容要求 | 证明完成的测试或观测 |
| --- | --- | --- | --- | --- |
| R0 基线冻结 | Step 1–12 Workspace 验收、运行/协议/隐私回归样本及功能 flag 清单 | 有可比较的重构前行为；确认优化不修补未完成功能 | 所有阻断项在轨道 A 先解决，保存非敏感证据，记录旧报告/pending 样本 | 完整本地回归 + Workspace smoke；固定版本案例可重复、CozeLoop 关闭时本地行为一致 |
| R1 数据与隐私契约 | 受限原件/白名单报告、外传字段目录、授权审计；`eval` 报告和 `cozeloop` 存储 | 防旁路泄露，单点定义外传字段 | 旧格式只读迁移，CI artifact 路径明确；不丢已授权的真实内容能力 | 注入敏感 Tool 参数、Judge 理由、错误、Prompt，报告/日志/CI 无泄露；授权撤销后不继续发送 |
| R2 身份与状态 | 运行身份值对象；同步/实验恢复状态与类型化错误；CLI 薄化 | 失败可定位、崩溃可恢复，不靠文本/目录约定 | 保持同 run 同内容重试、旧 pending 兼容和多 run 隔离 | 重复/跨 run/部分写入/未知 POST/并发/崩溃各阶段故障注入；报告本地质量不变 |
| R3 装配与 Trace | server/eval 共用能力装配，明确进程级 Handler 所有权，追踪上下文解耦 | 避免配置分叉/全局单例污染测试 | SDK 关闭、metadata-only 和授权路径不退化 | 不同客户端注册冲突、关闭/重入、回调分层、Candidate/Judge ID/Usage 流式测试 |
| R4 Prompt 审计 | 选择与解析契约、hash 口径、strict/online 测试 | 固定版本身份真正可审计；减少重复转换 | 不迁移 Tool 业务规则；报表 schema 兼容或明确升级 | 版本优先级/Workspace/标签/缓存、回退/取消、实际版本与内容摘要测试；关键路径性能基线 |
| R5 平台用例收敛 | 数据集/评估器/实验/A-B 的用例服务与共享安全传输实现 | 调用方无需拼远端动作顺序；新增 API 修改局部化 | 保留窄 port，不合并万能 Client；适配旧 Workspace 行为 | fake HTTP 对每个接口 DTO/授权/分页/错误回归；Workspace 小规模端到端复核 |
| R6 CI/文档收口 | 机器可读状态、脱敏产物、操作文档/监控 | 人和自动化对结果判断一致 | 普通 PR 无密钥可运行；平台分数 advisory | PR/可信作业/无密钥/撤销/失败产物审查；Step 1–12 验收引用仍可访问 |

其中 R1 的泄露风险、R2 的跨 run 错配、R5 所需的实验正确性如果在接入过程中已经出现，**必须在对应功能上线前处理**；后期只优化结构、降低重复，不能等完整接入后才修安全问题。R1–R6 是可讨论的切片，不强制新增所有列出的包或类型。

## 6. 验收标准与 review 清单

**整体优化完成的判断：**

- [ ] 每个接入能力都有唯一清晰的装配/关闭责任；全局 Trace Handler 的重复配置和生命周期有定义、可测试。
- [ ] Prompt 的 requested/resolved/version/source/fallback/hash 含义唯一，线上回退与严格评测分别回归。
- [ ] 数据集 schema/写入/评估器 Debug/实验映射的字段来源与敏感范围可从一个目录追踪；不上传未声明字段。
- [ ] 同一运行的本地结果、受限 pending、数据集快照、评估器版本、实验逐项结果可由身份/版本证据串联；部分失败不覆盖本地质量。
- [ ] 授权撤销、Token/错误脱敏、白名单 CI 产物由自动化和目标 Workspace 证据共同覆盖；无法核验内容就明确失败。
- [ ] CLI 入口不承载业务状态机；主要用例通过可注入 port 测试，fake HTTP 验证平台 DTO，真实 Workspace 证据独立维护。
- [ ] 旧报告、legacy pending 和无 CozeLoop 的本地模式仍可用，格式迁移与回滚边界有文档；Go tests、vet 与差异检查通过。
- [ ] 没有为了“代码整齐”做未经测量的缓存、泛化传输框架或不必要的万能 Client。

**仍需 review 的方案决策：**① 原始报告和脱敏摘要的保留期限/访问者；② 通用追踪上下文最终落点以及是否能在当前 Eino Callback 生命周期下消除进程级单例；③ Prompt 模板 hash 与逐次渲染 hash 的产品用途和保留范围；④ CI 授权主体与可信作业安全机制。未定事项保留在独立优化 OpenSpec 的 open questions，不把假设转为实现事实。

**本阶段交付：**仅该代码优化方案与 [Step 10–12 实施设计](cozeloop-step10-12-technical-design.md) 的关系澄清。下一步先 review“全链路接入 → 基线冻结 → 整体优化”的先后顺序、上述结构目标和风险分层；获确认后再逐切片产出可执行 OpenSpec 与测试计划，并在任何代码改动前另行请求确认。
