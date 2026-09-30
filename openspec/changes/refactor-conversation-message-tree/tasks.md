# Tasks

## Status and Phase Convention

`[MVP] / [Phase 2] / [Phase 3]` 是产品阶段；下列 Phase 0/1/2/... 是 **Implementation Phase**。checked 仅保留已实现的 Phase 1 基础代码/测试，不表示已接聊天、完成 Run/Projection/Compaction 或本轮已重新运行测试。新增复合规划任务仍 unchecked。经独立授权已实现 Phase 2 切片 1 契约基础，但其所属复合任务尚缺 schema/adapter；进度见 Phase 2 段。后续代码/migration 切片仍需独立授权。

范围见 [proposal](proposal.md)、[design](design.md)；实施 Phase 2 细节见 [AgentRun Technical Design](../../../docs/phase2-agent-run-technical-design.md)。

## Phase 0: Contract Review

- [ ] Task: [MVP / Phase 2] 验收阶段与跨模块契约一致性
  - Acceptance: 四个 module id、依赖、产品/实施编号、单 active leaf、安全投影 MVP 强制边界一致；Phase 1 只含 message 三角色基础，Phase 2 只做 Run 集成且保留旧模型 history，Phase 3 才切 Projection。
  - Verify: 手工逐项核对 proposal/design/tasks 与四份 spec；无 system/thinking/media/Compaction/artifact 已完成声明，无 Entry append 请求幂等、通用 RunEvent 表、公共分支或原始协议重建承诺。OpenSpec CLI 不可用，不能宣称 CLI validate 通过。
  - Files: `proposal.md`、`design.md`、`tasks.md`、`specs/*/spec.md`

- [ ] Task: [MVP / Phase 2] 验收事务与迁移矩阵并完成独立代码开工 review
  - Acceptance: 明确 Entry 事实、Run 引用、messages 可见正文、Hub 展示和旧摘要的 source of truth；BeginTurn/成功收尾/结果 batch/失败回退各自共享事务；恢复先于服务；artifact/TTL 独立可选。
  - Verify: 对照详细 Phase 2 设计和迁移矩阵 review；未知 head 不自动修补，旧 client id 不伪造回填。设计批准不等于本轮代码授权。
  - Files: `design.md`、`tasks.md`、相关技术设计文档

## Phase 1: Entry Tree Foundation

- [x] Task: [MVP] 定义 message Entry、head、工具关联与安全白名单基础契约
  - Acceptance: 已实现稳定 ID、parent、payload version、user/assistant/toolResult、text/toolCall、独立结果关联、版本化 SafeToolCall/SafeToolResult、白名单投影及路径/工具组校验；不含 system/thinking/media、Compaction、artifact 或 Draft。
  - Verify: 已有 domain/codec/security 测试覆盖消息结构、调用配对/次序、pending/closed path、未知版本、字段过滤与有界 preview；本任务不声称真实工具或聊天已接线。
  - Files: `backend/internal/domain/conversation/entry*.go`、相关测试、`backend/internal/infrastructure/repository/sqlite/entry_tree_codec.go`

- [x] Task: [MVP] 实现独立 Entry Tree port、SQLite repository 与基础 migration
  - Acceptance: 已实现 GetHead/Get/ReadPath/ListChildren/Append/MoveHead、同会话约束、leaf+version CAS/ABA、防原地改写、COMMIT 失败回滚与会话整体删除；旧聊天未接入新树，无 client id 幂等 append 声明。
  - Verify: 已有 SQLite/存储测试覆盖空 head、完整路径、内部分支、闭合移动点、并发 CAS、外键/索引、回滚、corruption/容量与删除；代码/测试存在不等于本轮重新执行通过。
  - Files: `backend/internal/domain/conversation/entry_store.go`、`backend/internal/infrastructure/repository/sqlite/entry_tree*.go`、`backend/internal/infrastructure/repository/sqlite/sql_test/entry_tree*_test.go`、`backend/internal/infrastructure/storage/migrations/0008_conversation_entry_tree.sql`

## Phase 2: AgentRun Lifecycle and Atomic ChatTurn Integration

本实施阶段属于产品 MVP，尚未完成功能交付。模型仍使用旧线性 History/旧摘要与当前 Run 内存工具循环；不接 Projection、compaction、artifact、Draft、通用持久化 RunEvent、SSE replay/resync/revision 或公共分支 API。

切片 1 进度：已新增 `domain/agentrun` 领域模型/状态机/结构校验与测试，以及聊天 GenerationStore/RunRecorder/RecoveryStore 契约、Run/完整最终消息/token 与深拷贝测试。定向及 `cd backend && go test -count=1 ./...` 通过；race 因未启用 CGO 未运行。当前仍使用旧 TurnStore 生产路径，无 migration、具体 Recorder、事务 adapter 或运行装配改造；首条复合任务保持 unchecked。细节见技术设计 §13.6，完成切片 1 review 后再单独批准切片 2。

- [ ] Task: [MVP] 定义 reference-only Run contract、repository port 与新增 schema
  - Acceptance: 包含 base_entry_id/base_head_version、user/last/last_closed/final entry、head_version、legacy user/assistant IDs、client id、状态、脱敏错误及时间，无 body/raw tool JSON；FinalEntryID 仅 completed 存在。
  - Verify: 状态/引用单测；conversation+client id 唯一索引、active conversation 部分唯一约束、assistant Message 查询关联与恢复查询索引测试；复合归属、跨会话拒绝及整体删除不被新 FK 阻塞。
  - Files: `backend/internal/domain/`、`backend/internal/application/chat/`、SQLite repository、新增 migration 与测试

- [ ] Task: [MVP] 在同一事务接入 BeginTurn 并保持幂等优先
  - Acceptance: 先查 Run/旧 Message pair，再检查 active Run/legacy streaming busy；新请求的 user Entry、旧消息对、pending Run、base token 与 head all-or-nothing；初始 last=last_closed=user。只有旧对无 Run 的 client id 必须只复用，不执行、不创建 Run/Entry。
  - Verify: 新/旧/终态/活跃幂等、另一请求 busy 时旧 id 仍复用、跨会话相同 id、并发新 id、关联损坏、CAS/INSERT/COMMIT 故障注入；失败零残留且不启动模型。
  - Files: `backend/internal/infrastructure/repository/sqlite/chat_turn_store.go`、tx-local Entry/Run helpers、chat types/ports、测试

- [ ] Task: [MVP] 实现 active head 不变量与统一失败回退事务
  - Acceptance: active LastEntryID/HeadVersion 匹配 head；LastClosedEntryID 只在闭合点推进；failed/cancelled/recovery 的 pending group 分支保留，CAS 移回 LastClosedEntryID，LastEntryID 保留最后事实，HeadVersion 为操作后版本；闭合组 head 不变，FinalEntryID=nil。
  - Verify: 全缺/部分结果失败、闭合组后失败、初始 user 闭合点、ABA/CAS 冲突、损坏 target、事务回滚；无节点删除/改 parent、无 fake cancelled result、无部分失败 assistant Entry，终态 LastEntryID 可不同于 head。
  - Files: Run/ChatTurn repository、domain contracts、生命周期测试

- [ ] Task: [MVP] 增加可传播错误的完整 assistant 消息控制 hook
  - Acceptance: 从完整模型响应取得有序 text/toolCall，工具执行前安全投影并提交 assistant call/head/Run waiting_tool；不把 Graph 显示回调、SSE 摘要或 checkpoint 当事实。无调用的最终响应交给原子成功收尾，不提前单独插入最终 Entry，也不伪造适配器未提供的 block 次序。
  - Verify: 完整消息含多 call/多 block、流式参数收敛、模型中断、显示摘要信息不足、投影/持久化失败时工具未执行；hook 错误传播不被 callback 吞掉。
  - Files: `backend/internal/agent/interview/builder.go`、`backend/internal/agent/eino/`、chat executor/ports、集成测试

- [ ] Task: [MVP] 将逐工具安全策略接到真实事实/显示边界
  - Acceptance: 实际工具注册并 review 版本白名单；只持久化有界安全 call/result，raw 参数/结果只供当前内存执行，不进入 Entry/Run/messages/log/diagnostic/backup/SSE；preview 来源为批准字段，不提供 raw fallback。
  - Verify: 未注册策略、未批准字段、字段值凭据、原始错误、超限参数、UTF-8 结果截断、安全编码回读与 Entry/Run/旧消息/日志/SSE 隐私断言；SafeToolCall 不被当作可重放 arguments。
  - Files: 工具策略与 adapter、chat/agent 编排、安全测试

- [ ] Task: [MVP] 原子批次提交独立工具结果并控制下一模型调用
  - Acceptance: 完整结果按 assistant 声明次序成为独立 Entry，单事务提交全部结果/head/Run；闭合后 last_closed=last、状态回 running。失败批次全回滚并阻断下一模型调用，不自动重放或制造缺失结果。
  - Verify: 并行逆序完成、多工具、真实工具错误、重复/错配/缺失结果、每个 INSERT/CAS/COMMIT 失败、取消中的迟到结果、超工具循环上限；下一模型只能在提交成功后继续。
  - Files: Graph 控制 hook、Run/Entry tx-local batch helpers、工具集成测试

- [ ] Task: [MVP] 保留旧文本 checkpoint 并实现原子成功收尾
  - Acceptance: checkpoint 只更新 streaming Message，无 Draft/可变 Entry/Run body；扩展现有 FinishAssistant，flush 后一次事务提交完整最终 assistant+head+completed Run（含 final/last/last_closed/head_version）+legacy assistant terminal；COMMIT 后才广播终态，不要求新公开 API。
  - Verify: 长文本 checkpoint、最终无工具响应、工具循环后最终响应、相同终态（包括 head 已回退）先复用而不重复追加/不同终态 conflict、Recorder/BeginTurn/恢复 token 传递、缺失/失效 expected token、最终 Entry/Run/旧终态/COMMIT 失败、先广播禁止断言；保留旧可见文本且不以原始工具对象填正文。
  - Files: chat service/writer/sink、ChatTurn repository、生命周期测试

- [ ] Task: [MVP] 处理 Start 注册/MarkRunning 窗口并补偿启动失败
  - Acceptance: 尚未启动 goroutine 时 Hub/Registry 注册、MarkRunning 或启动准备失败必须 failedRun+旧 read model 原子收敛，按统一 head 策略处理并清理本次资源；注册和 running 状态提交后才启动模型；Start/Cancel 短窗口串行且等待不持锁；补偿失败明确报错。
  - Verify: 注册/状态提交故障、窗口中旧 id 重试/Cancel 竞争、资源清理、补偿存储故障、原 client id 复用失败快照、新 id 可再次开始、无模型执行；该分支不作为 HTTP Cancel 的第二个 finalizer。
  - Files: `backend/internal/application/chat/service.go`、registry/hub、Run store、测试

- [ ] Task: [MVP] 实现 detached Cancel 与唯一 generation finalizer
  - Acceptance: HTTP Cancel 只发信号并等待/重读，generation goroutine 负责停止迟到写、独立收尾上下文 flush、唯一终态事务和提交后广播；已观察取消不再成功，已成功提交不被迟到取消覆盖；执行继承进程 root 关闭信号而非浏览器请求取消。
  - Verify: 浏览器断连不中止、进程 root 取消/关闭顺序、重复取消、取消/模型完成/错误竞争、终态后 hook/checkpoint/event 拒绝、事务失败不广播成功、已闭合 head 保持和 pending group 回退。
  - Files: chat service/generation/registry、Run transitions、并发测试

- [ ] Task: [MVP] 实现单实例所有权下的服务前置恢复
  - Acceptance: 取得进程所有权后、开放请求前原子收敛 active Run+streaming read model 为 failed/generation_interrupted，保留正文；legacy orphan 同样失败但无 Run/Entry backfill。未知/不一致 head、引用或恢复存储失败必须阻止启动，不恢复模型/工具。
  - Verify: pending/running/waiting_tool 重启、闭合保持/pending 回退、orphan 文本、重复恢复、跨会话引用、未知 head/version、恢复故障阻止就绪、单实例冲突、旧 client id 重试只复用。
  - Files: 启动装配、ownership/recovery、SQLite repository、测试

- [ ] Task: [MVP] 保持 assistantMessageID HTTP/SSE 与安全展示契约
  - Acceptance: 公共 Start/Subscribe/Cancel IDs 保持；Hub 同锁快照+订阅、有限 step 摘要、终态 Message 快照和慢客户端非阻塞策略保持；不持久化通用 RunEvent，不新增 replay/resync/revision，不凭 Hub 缺失判 generation_interrupted。
  - Verify: 活跃/终态/重启订阅、慢客户端重新取快照、运行中缺失 Hub/Registry、ID/role/会话隔离、无 raw tool/think/credential/prompt 泄露、无虚构历史工具卡片或可靠事件投递承诺。
  - Files: chat generation/service、HTTP stream 编码与兼容测试

- [ ] Task: [MVP] 完成 Phase 2 原子集成与旧模型路径回归验收
  - Acceptance: 核心 Entry+Run+messages 新请求集成在本阶段完成，不推迟至 cleanup；现有模型读取仍为旧线性历史，尚未开放公共分支；所有新 Run 任务有代码/测试证据后才勾选。
  - Verify: ChatTurn 故障矩阵、SQLite migration/删除会话、HTTP/SSE、工具迭代、并发终态、隐私断言和 `cd backend && go test ./...`；不因基础测试存在而标记 Run 已实现。
  - Files: chat/agent/repository 集成测试、验收记录

## Phase 3: Context Projection

- [ ] Task: [MVP] 构造一致 active-path 与 provenance-preserving projection
  - Acceptance: 只读取合法完整 root-to-leaf；每条消息可追溯 Entry/版本/策略，工具事实保留关联与截断/降级 metadata；失败分支不混入历史；当前阶段不实现 compaction/context edit 等未来类型。
  - Verify: 内部分支选择、空 root、跨会话/缺失/循环/超容量/未知版本、closed/pending path、安全来源查询测试。
  - Files: `backend/internal/agent/context/`、domain/context ports 与测试

- [ ] Task: [MVP] 定义安全历史转换策略并实现模型适配器边界
  - Acceptance: 安全字段不是原始参数，无法合法表达就明确拒绝或采用已批准的整组事实文本降级；保持 provenance/错误/次序，不承诺原始协议重建，不自动执行历史工具。适配器无静默丢失，无 thinking/media MUST。
  - Verify: 普通文本、多工具安全组、截断/字段丢弃、未批准映射拒绝、批准文本降级、孤立/重复/缺失结果、不可支持 block 和模型边界测试；完整当前 Run 内存协议与历史投影区分。
  - Files: context converter、`backend/internal/agent/eino/`、context/adapter 测试

- [ ] Task: [MVP] 切换 ContextManager seam 并显式兼容 legacy history
  - Acceptance: Phase 3 才从旧 History 切换 Projection；旧文本使用真实 legacy 来源和明确迁移水位，避免遗漏/重复旧历史及重复本次 user；不从重试、旧摘要或显示事件伪造 Entry/工具事实。
  - Verify: manager/executor 集成、新树/纯旧/混合历史、受控兼容原因、去重水位、一致性失败、预算内 legacy text 测试；调用方不绕过 context seam 自选历史。
  - Files: chat context/types、`backend/internal/agent/context/`、executor、测试

## Phase 4: Future Context Compaction

以下属于产品 Phase 2，不属于实施 Phase 2 Run。

- [ ] Task: [Phase 2] 实现最终预算与完整逻辑组选择器
  - Acceptance: 每次模型调用校验输入估算+输出预留+安全余量<=窗口，包括运行期 system/tool schema 开销但不持久化 prompt 原文；按完整用户轮次/工具组选择安全前缀，保护当前请求和 pending group；artifact 非前置。
  - Verify: 中文/代码/工具增长、unknown window、超大单组、有界安全结果、无合法边界容量错误和每次调用复检。
  - Files: `backend/internal/agent/context/`、预算/选择器测试

- [ ] Task: [Phase 2] 实现 compaction 生成、来源校验与原子树提交/复用
  - Acceptance: 只总结选定当前路径安全事实，schema/source boundary/tool protocol/预算和 leaf+version CAS 成功才追加 compaction；失败不推进，原始事实保留，重启复用合法摘要。不读取失败分支/raw 工具/think/credential。
  - Verify: 模型失败、空/超预算/非法摘要、跨分支来源、CAS/存储冲突、多 compaction、无 pending 切割、重启复用；未知摘要拒绝/重建。
  - Files: context summary/compaction、Entry 版本/schema 扩展、新增 migration 与测试

- [ ] Task: [Phase 2] 明确旧 covered_sequence 摘要迁移
  - Acceptance: 旧线性摘要只通过显式 legacy mapping 或失效重建进入新 Projection，不伪装为树 Entry/path 覆盖水位；保持隐私与来源边界。
  - Verify: sequence 间隙、未回填历史、已有/无 summary、迁移中断、模型配置变化与重建/容量错误。
  - Files: context summary repository/converter、迁移方案与测试

## Optional Follow-up: Restricted Artifact Capability

- [ ] Task: [Phase 2] 在核心 Run/Projection 后独立批准和实施 artifact/TTL
  - Acceptance: 可选能力有不透明 ID、会话/Run/call 隔离、受限读取额度、TTL、原子发布和清理；不提供 raw tool/凭据/think 持久化旁路，永久 Entry 只保留有界安全事实。不属于基础或本次 Run 开工任务。
  - Verify: 独立 scope/privacy review，过期、跨会话、额度、物理路径隔离、发布回滚和清理；未启用时 MVP 安全 preview/truncated/容量失败仍可工作。
  - Files: 待独立 review 的 artifact ports/storage/tests，不预建无调用方表

## Phase 5: Compatibility and Cleanup

- [ ] Task: [MVP / Phase 2] 验收显式历史迁移，不延后核心新请求集成
  - Acceptance: 核心双写已在 Phase 2 原子完成；旧 user/assistant text 历史迁移需明确水位、去重、失败状态与独立批准，不在旧 client id 重试中制造 Run/Entry，也不伪造工具协议。旧 read model 保留可用。
  - Verify: 新/旧/混合历史、迁移中断/重试、旧 API/会话删除、summary source boundary、受控兼容回退理由。
  - Files: 显式迁移/回填方案、repository/context、兼容测试

- [ ] Task: [MVP / Phase 2] 验收后独立评估旧模型路径清理
  - Acceptance: Projection 已成为通过验收的新历史来源；旧路径删除需另行批准，不隐含删除 messages。兼容回退不绕过来源/工具协议/安全检查，不承诺直接回滚旧二进制。
  - Verify: 纯旧/新树/混合/安全工具/未来压缩会话端到端，migration 版本与备份/回滚 review。
  - Files: chat/context/executor、兼容方案与测试

- [ ] Task: [MVP / Phase 2] 完成文档、规范与分阶段代码验收
  - Acceptance: 每项适用 MUST 有测试或可观测证据；未来 compaction/artifact 和未实现 Run/Projection 不标完成；不存在 Draft、通用 RunEvent 表、SSE 重放、自动工具重放或 thinking 持久化隐含范围。
  - Verify: 手工 OpenSpec 一致性/链接检查、`cd backend && go test ./...`、SQLite migration、HTTP/SSE、隐私扫描和人工 review；CLI/工具链不可用必须如实记录。
  - Files: 本 change artifacts、技术设计与测试报告
