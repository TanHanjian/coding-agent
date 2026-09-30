# Agent Run Specification

## Scope

本规范是产品 MVP、实施 Phase 2 的待实现契约。既有 Phase 1 只提供树基础，不表示 Run、原子 ChatTurn 集成、恢复或完整工具持久化已实现。实施 Phase 2 模型 MUST 继续读取旧线性 History；切换 Projection 属于实施 Phase 3。参见 [design](../../design.md)、[tasks](../../tasks.md) 和 [Entry Tree spec](../entry-tree/spec.md)。

## Requirements

### Requirement: [MVP] Submission idempotency precedes busy checks

系统 MUST 以 `conversationId + clientMessageId` 唯一识别新提交的 AgentRun。相同 client id MUST 在 busy 检查前复用已有 Run/旧 Message 对，不追加用户事实或启动第二次执行。只有旧 Message 对而无 Run 的历史请求 MUST 原样复用，不执行、不伪造 Run/Entry 回填；不一致映射 MUST 明确失败。

#### Scenario: Retry an existing Run while another request is busy

- **WHEN** 相同 client id 已有 Run/Message 结果，而会话当前被另一个 Run 占用
- **THEN** 系统 MUST 返回该 client id 的既有结果快照，不返回针对新提交的 busy
- **AND THEN** 不得新增 Entry、Message 或执行

#### Scenario: Retry a legacy pair without a Run

- **WHEN** client id 只关联迁移前旧 user/assistant Message 对
- **THEN** 系统 MUST 返回该消息对，明确是复用且没有 Run
- **AND THEN** 不得执行模型或凭旧文本伪造 Run、工具事实、Entry backfill

#### Scenario: Detect an inconsistent idempotency mapping

- **WHEN** 既有 Run 的 Message 引用缺失/跨会话，或旧消息对不完整
- **THEN** 系统 MUST 返回一致性错误，不创建替代提交或修补虚假历史

### Requirement: [MVP] Atomic new-turn creation

仅对新 client id，BeginTurn MUST 在同一事务中检查 busy 和合法闭合 head，记录 base entry/version，创建 user Entry、旧 completed user/streaming assistant 对、pending Run 并推进 head。以上 MUST all-or-nothing，MUST NOT 以分别提交或异步补写宣称原子成功。

#### Scenario: Begin a new turn

- **WHEN** 新 client id 提交到空闲且一致的会话
- **THEN** 系统 MUST 原子创建上述记录，BaseEntryID/BaseHeadVersion MUST 等于 user 追加前的 head
- **AND THEN** UserEntryID、LastEntryID、LastClosedEntryID MUST 初始相同，HeadVersion MUST 为操作后版本，FinalEntryID MUST 为空

#### Scenario: Fail before commit

- **WHEN** user Entry、旧 Message、Run、head CAS 或 COMMIT 任一失败
- **THEN** 整个新提交 MUST 回滚，不残留 user、streaming assistant、pending Run 或新 head
- **AND THEN** Start MUST 不返回成功或启动后台执行

### Requirement: [MVP] One isolated active Run per conversation

同会话 MUST 至多一个 pending/running/waiting_tool Run；新 client id 遇到 active Run 或 legacy streaming assistant MUST 返回 conflict/busy 且零写入。持久化 MUST 提供 conversation+client id 唯一性与 active conversation 唯一性约束及相应查询索引。不同会话 MUST 可独立执行；Run 的 Entry/Message 引用 MUST 同会话隔离，活跃期间 MUST 禁止外部 MoveHead。

#### Scenario: Compete with two new client ids

- **WHEN** 两个新 client id 同时开始同一会话
- **THEN** 只能一个创建 active Run，另一个 MUST 返回 conflict/busy
- **AND THEN** 不得出现交叉 parent、孤立 user 或两个 streaming read model

#### Scenario: Reuse a client id in another conversation

- **WHEN** 不同会话使用相同 client id，或请求给当前 Run 设置另一个会话的 Entry/Message 引用
- **THEN** client id MUST 按会话作用域独立匹配，跨会话引用 MUST 拒绝
- **AND THEN** 返回或取消路径 MUST 不泄漏或影响其他会话结果

### Requirement: [MVP] Reference-only lifecycle and head invariants

Run MUST 支持 pending/running/waiting_tool/completed/failed/cancelled，并原子收敛到唯一终态。Run MUST 保存 base_entry_id、base_head_version、user_entry_id、last_entry_id、last_closed_entry_id、final_entry_id、head_version、legacy user/assistant IDs、请求身份、脱敏状态/错误与时间，MUST NOT 保存消息 body、模型 history 或 raw 工具正文。active Run 的 LastEntryID/HeadVersion MUST 匹配当前 head；LastClosedEntryID MUST 是最近已提交的闭合点；FinalEntryID MUST 仅在成功时存在。

#### Scenario: Enter and close a tool group

- **WHEN** assistant call 事实成功提交，随后完整结果 batch 成功提交
- **THEN** Run MUST 从 running 进入 waiting_tool 再回到 running，每次原子更新 LastEntryID/HeadVersion
- **AND THEN** pending 时 LastClosedEntryID MUST 保持不变，组闭合时 MUST 推进至组末端

#### Scenario: Reject a stale active head

- **WHEN** 写入发现实际 head 与 active Run 的 LastEntryID/HeadVersion 不匹配
- **THEN** 系统 MUST 返回一致性/CAS 错误，不得自动改挂 parent 或刷新 expected token 继续写入

### Requirement: [MVP] Full-message persistence controls execution

系统 MUST 使用可传播错误的完整消息控制 hook，不得以 Graph 显示 callback、SSE 摘要或文本 checkpoint 代替事实输入。完整 assistant call MUST 先经安全投影并提交，之后才允许工具执行；完整工具结果 MUST 独立成 Entry，按声明顺序在一个事务批次中提交，之后才允许下一次模型调用。原始参数/结果 MUST 仅留在本次执行内存边界，MUST NOT 进入 Run、Entry、旧 Message、日志、诊断、备份或 SSE。

#### Scenario: Persist out-of-order parallel results in declaration order

- **WHEN** assistant 声明 c1/c2，而 c2 先完成
- **THEN** 完整结果 batch MUST 校验数量/身份并按 c1/c2 次序提交两个独立 Entry、head 和 Run 状态
- **AND THEN** 任一结果、CAS 或 COMMIT 失败 MUST 回滚整个 batch，不继续模型调用

#### Scenario: Fail the assistant call commit

- **WHEN** 完整调用的投影、校验或持久化失败
- **THEN** 控制 hook MUST 将错误传播给执行器，工具 MUST 不执行
- **AND THEN** 系统 MUST NOT 从已发送的显示事件重建事实或忽略错误继续循环

#### Scenario: Reject incomplete or excessive execution

- **WHEN** 工具结果缺失/重复，或当前执行超出既有工具循环上限
- **THEN** Run MUST 以脱敏错误收敛，不得越过 pending group 或自动重放工具

### Requirement: [MVP] Text checkpoints and atomic successful completion

流式可见文本 MUST 只 checkpoint 到旧 streaming Message，MUST NOT 创建 Draft、可变 Entry 或 Run 正文。成功最终 assistant MUST 来自完整响应；最终 Entry/head、completed Run（含 FinalEntryID）与 legacy assistant completed MUST 在同一成功收尾事务提交。现有终态写入契约 MUST 承担该原子收尾，不要求新的公开 API；终态广播 MUST 在成功提交之后。相同终态重试 MUST 复用既有结果而不重复追加，不同终态请求 MUST 返回 conflict 且不覆盖已提交结果。

#### Scenario: Finish a successful generation

- **WHEN** generation goroutine 已得到完整无 pending group 的最终响应并完成可见文本 flush
- **THEN** 系统 MUST 原子追加最终 assistant、更新 head、LastEntryID/LastClosedEntryID/FinalEntryID/HeadVersion 和旧 assistant 终态
- **AND THEN** 事实 hook MUST NOT 先单独提交最终 Entry，再用另一个事务补完成状态

#### Scenario: Fail a completion transaction

- **WHEN** 最终 Entry、Run 更新、旧终态或 COMMIT 失败
- **THEN** 完成事务 MUST 全部回滚，不广播成功 terminal
- **AND THEN** 既有可见 checkpoint MUST 保留，后续失败收敛不得将其伪装为成功 FinalEntryID

#### Scenario: Reject stale terminal intent

- **WHEN** 新终态写入使用的提交方 expected leaf/version 与 active Run 引用或实际 head 不一致，或未提供所需 token
- **THEN** 系统 MUST 拒绝整个终态写入，不得临时改用实际 head/version 后继续提交
- **AND THEN** 普通执行收尾的 token MUST 来自本 Run 最新已提交快照，启动补偿来自 BeginTurn 结果，前置恢复来自持久化 active Run 快照

#### Scenario: Retry a terminal Run after rewind

- **WHEN** Run 已经终态收敛并回退 head，相同终态请求再次到达
- **THEN** 系统 MUST 先返回既有结果，不再次执行 active/head 写入校验或移动 head
- **AND THEN** 不得用终态 Run.LastEntryID/HeadVersion 重建新的可写 token

### Requirement: [MVP] Immutable failed branches and closed-head rewind

失败、取消或启动恢复 MUST 原子更新 Run 与旧 read model，保留所有已提交事实和可见文本。若 active path 有 pending tool group，终态事务 MUST 用 active Run 的 LastEntryID/HeadVersion 作 CAS，将 head 移到同会话、已验证的 LastClosedEntryID；若组已闭合，MUST 保持 head。LastEntryID MUST 保留最后写入事实，HeadVersion MUST 记录操作后的实际版本，failed/cancelled 的 FinalEntryID MUST 为空。系统 MUST NOT 删除/改 parent、制造 cancelled toolResult 或保存未收敛 assistant 事实。

#### Scenario: Fail with an incomplete group

- **WHEN** 已提交 assistant call 但部分或全部结果缺失，Run 需要失败/取消/恢复终止
- **THEN** 原不完整分支 MUST 保持不可变且可内部读取，active head MUST 原子回到 LastClosedEntryID
- **AND THEN** LastEntryID MUST 保持最后事实，允许与回退后的 active leaf 不同；HeadVersion MUST 是回退后版本，后续新提交 MUST 从闭合 head 开始

#### Scenario: Fail after a closed group

- **WHEN** 最近工具组已全部提交，之后模型失败或用户取消
- **THEN** 已闭合 head MUST 不回退，已提交 call/result MUST 保留
- **AND THEN** 可见失败部分文本 MUST 只保留在 legacy Message，不生成虚假的 final Entry

#### Scenario: Encounter an unknown rewind target or token

- **WHEN** LastClosedEntryID 不是可验证的闭合祖先，或实际 head/token 与 Run 不符
- **THEN** 收敛事务 MUST 失败且不猜测目标或重新挂 parent
- **AND THEN** 启动恢复遇到该问题 MUST 阻止服务启动

### Requirement: [MVP] Detached execution and ordered terminal ownership

后台执行 MUST 不依赖浏览器/SSE 请求的取消上下文，但 MUST 继承进程 root 关闭信号。显式 Cancel MUST 只发取消信号，已启动 generation goroutine MUST 是唯一执行 finalizer，负责隔离迟到写入、以独立收尾上下文 flush、终态事务及提交后广播。取消、成功和错误 MUST 有序竞争唯一终态；终态后所有事实、checkpoint 和显示写入 MUST 被拒绝。

#### Scenario: Cancel competes with completion

- **WHEN** 显式取消与完整响应收尾竞争
- **THEN** finalizer 已观察取消后 MUST 不再提交成功；成功已提交时迟到取消 MUST 不覆盖结果
- **AND THEN** HTTP Cancel MUST 等待或读取持久化终态，不直接调用第二个终态写入流程

#### Scenario: Disconnect a browser

- **WHEN** 浏览器刷新或 SSE 断开但未显式取消
- **THEN** Run MUST 继续由独立执行上下文管理，不因断连创建失败终态或启动第二次执行

### Requirement: [MVP] Compensate a failed start registration

BeginTurn 已提交但尚未启动 generation goroutine 时，Hub/Registry 注册、pending→running 或启动准备失败 MUST 触发补偿，将 Run 与 legacy streaming assistant 原子收敛为 failed，并按同一闭合 head 策略处理。系统 MUST 在注册及 running 状态提交成功后才执行模型；Start 注册窗口与 Cancel 信号操作 MUST 串行化，等待终态 MUST 不持该窗口锁。系统 MUST 清理本次内存资源且不得留假 streaming/busy；补偿失败 MUST 明确报错，不返回成功。

#### Scenario: Fail registration after BeginTurn

- **WHEN** 新提交已落库，但运行时注册失败且 goroutine 未启动
- **THEN** 系统 MUST 不调用模型，并提交失败补偿及 read model 终态
- **AND THEN** 相同 client id MUST 返回既有失败快照，不再执行或新建事实

#### Scenario: Fail compensation storage

- **WHEN** 注册失败补偿无法提交
- **THEN** Start MUST 返回明确失败，不广播持久化成功终态
- **AND THEN** 遗留记录 MUST 受服务前置恢复约束，不能因 Hub 缺失静默重建执行

### Requirement: [MVP] Single-owner recovery before serving

服务 MUST 先取得单实例进程所有权，再在开放请求前执行恢复。遗留 pending/running/waiting_tool Run 及其 streaming read model MUST 原子转为 `failed/generation_interrupted`，按闭合 head/未闭合回退策略处理并保留可见正文。没有 Run 的 legacy streaming orphan MUST 同样转为该失败，不伪造 Run/Entry。未知/不一致 head、归属错误或存储失败 MUST 阻止启动；系统 MUST NOT 恢复模型或工具执行。

#### Scenario: Recover an interrupted pending group

- **WHEN** 旧实例在 pending/running/waiting_tool 状态退出
- **THEN** 新实例 MUST 在提供服务前校验引用并收敛 Run/旧 assistant，必要时 CAS 回退闭合点
- **AND THEN** 相同 client id MUST 返回既有失败，新 client id 才能开始新执行；重复恢复 MUST 不追加事实或再次移动 head

#### Scenario: Recover a legacy orphan

- **WHEN** 启动发现旧 streaming assistant 没有关联 Run
- **THEN** 系统 MUST 保留文本并标记 failed/generation_interrupted，不补造事实、不移动无所属证明的 head

#### Scenario: Reject inconsistent recovery or competing ownership

- **WHEN** 不能取得单实例所有权，或 Run 引用/head/token 无法验证，或恢复事务失败
- **THEN** 服务 MUST 不进入可服务状态，不猜测新 parent 或接受新请求掩盖问题

### Requirement: [MVP] Existing assistant-message-addressed HTTP and SSE

Start/Subscribe/Cancel MUST 保留现有 Message IDs 和 assistantMessageID 寻址，RunID MUST 不成为客户端迁移前置。系统 MUST 保留同锁注册订阅者与复制内存快照、先快照后增量、有限安全 step 摘要和非阻塞慢订阅者行为。展示事件 MUST NOT 进入模型上下文或通用持久化 RunEvent 表；本阶段 MUST NOT 增加 SSE replay/resync/revision 协议或宣称可靠投递。

#### Scenario: Subscribe to active or terminal content

- **WHEN** 客户端以 assistantMessageID 订阅
- **THEN** 活跃 Hub MUST 原子返回可见快照和后续流；终态/重启后 MUST 返回持久化 Message 快照
- **AND THEN** 不得由旧正文/摘要伪造完整工具卡片或泄漏 raw tool、think、凭据、prompt/原始错误

#### Scenario: Handle a slow subscriber

- **WHEN** 订阅者消费不及时
- **THEN** 模型执行 MUST 不被无限阻塞，客户端可重新取得当前快照
- **AND THEN** 系统 MUST 不承诺补放每个丢失事件或引入 after-sequence 数据库重放

#### Scenario: Observe a missing runtime handle while serving

- **WHEN** Subscribe/Cancel 找不到 Hub/Registry，而持久化状态尚未终止
- **THEN** 系统 MUST 只返回持久化快照或明确运行时不可用错误，不单凭句柄缺失推断 generation_interrupted
- **AND THEN** 不得触发隐式恢复、自动重执行或改挂 head
