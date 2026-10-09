# Design

## Context

动机与总体范围见 [proposal.md](proposal.md)。本变更只替换 Go 后端持久化实现，兼容性验收标准集中在本文；`.openspec.yaml` 使用 `skip_specs: true`，不新增或修改产品 capability。

### Current Implementation

- `backend/internal/infrastructure/storage/sqlite.go` 当前通过 `modernc.org/sqlite` 打开 `*sql.DB`，只保留一个活动/空闲连接，配置 `foreign_keys=ON`、`busy_timeout=5000`、WAL，并提供 `WithinTx(ctx, func(context.Context, *sql.Tx) error)`。
- `backend/internal/infrastructure/storage/migrate.go` 嵌入版本化 SQL，在一个事务中验证 SHA256 checksum、连续版本和支持上限，再应用迁移并显式提交。当前工作区包含 `0001` 至 `0009`，第九个迁移及 AgentRun 实现尚未提交。
- 已实现仓储中的 72 处直接 SQL 调用分布于 `question.go`、`answer.go`、`review.go`、`attachment.go`、`conversation_repository.go`、`chat_turn_store.go`、`agent_run_store.go`、`entry_tree_repository.go`、`entry_tree_path.go`。存储初始化与迁移另有 7 处；此数字是本次盘点基线，不是必须降至零的指标。
- 时间列存储 UTC RFC3339Nano 文本；Entry payload 是 TEXT JSON；标签单独存表。仓储负责把未找到、冲突、非法输入、损坏数据等转换成领域错误。
- 当前生产装配仍使用旧 ChatTurnStore。EntryTree 以及 AgentRun schema/BeginTurn 已有独立测试与组合事务内核，其余 Phase 2 生命周期及后续 Projection/Compaction 尚未完成。
- `context_summary_repository.go` 的 Get/Upsert 返回 `agentcontext.ErrSummaryStoreNotImplemented`，本次保持该边界。

### Existing Sources of Truth

- 普通记录、搜索及 CRUD：`backend/internal/infrastructure/repository/sqlite/sql_test/repository_test.go`、`sql_test.go`、`conversation_test.go`、`chat_turn_test.go`。
- EntryTree：`openspec/changes/refactor-conversation-message-tree/specs/entry-tree/spec.md` 与对应 `entry_tree_test.go`、`entry_tree_regression_test.go`。
- 当前 AgentRun 切片：原变更的 `tasks.md`、`docs/phase2-agent-run-technical-design.md`，以及 `agent_run_store_test.go`、`agent_run_migration_test.go`。原设计中的未来流程不因此成为本次任务。
- 事务组合：`chat_turn_tx_test.go`、`entry_tree_tx_test.go`、`entry_chat_tx_test.go`；包含语句失败与延迟外键导致的 COMMIT 失败覆盖。

## Goals / Non-Goals

**Goals:**

- [MVP / Phase 2] 仓储主流程表达业务操作及校验顺序，减少列扫描、占位符生成、完整 SQL 字符串及重复映射。
- [MVP / Phase 2] ORM 只位于基础设施内部；现有领域类型、应用端口和 API 继续适用。
- [MVP / Phase 2] 在同一物理连接和事务内完成原有原子操作，保留事务外可观察结果及错误分类。
- [MVP / Phase 2] 通过具名筛选、持久化模型、明确的原生 SQL 边界和兼容性测试，使后续改动容易 review。

**Non-Goals:**

- 不以 ORM 替代领域校验、数据库约束或事务状态机，不为所有仓储引入泛型基类。
- 不改变数据库表、列、索引、外键、触发器、存储格式或已有迁移 checksum。
- 不为搜索增加全文索引、调整 LIKE 通配符语义或排序规则，不扩充原有读事务的一致性承诺。
- 不修复既有 codec 的非法 UTF-8 漂移与 JSON 错误回显问题，不以这些独立缺陷的修复作为本变更通过条件。

## Decisions

### 1. Reuse the Existing Connection

采用 `gorm.io/gorm` 和官方 `gorm.io/driver/sqlite` dialector，继续由现有 `sql.Open("sqlite", ...)` 创建 modernc 连接。通过 `sqlite.New(sqlite.Config{Conn: sqlDB, DriverName: "sqlite"})` 初始化 ORM，不另开连接池，不默认使用 `sqlite.Open(...)` 创建 mattn 连接。

`storage.DB` 保留底层 `*sql.DB` 以服务 Close、Ping、连接池配置及直接数据库约束测试，另持有命名的 ORM 成员；不同时匿名嵌入两个 DB。根 ORM 句柄通过一个绑定 context 的方法取得，调用方不自行 `gorm.Open`。

配置约定：

- 保持最大活动连接、最大空闲连接均为 1，保留现有 PRAGMA 和打开失败清理流程。
- 关闭 ORM 的默认写事务；需要多语句原子性的操作全部使用明确的外层事务，原来单语句的操作保持单语句提交。
- 禁用自动数据库日志输出，使用 Silent logger；业务诊断只记录操作类型与安全错误分类，不输出 SQL 参数、消息正文或工具内容。
- 不开启隐式错误翻译、自动关联保存、自动 schema 迁移或全局预编译缓存。保留 modernc 原始错误链，再按现有仓储语义处理。
- GORM/driver 版本在接入时锁定到 go.mod/go.sum；首个实施切片先验证初始化、事务、RETURNING 和 `CGO_ENABLED=0` 的构建/运行。

官方 SQLite dialector 虽接收现有 Conn，仍会间接引入 mattn/go-sqlite3 包；不能仅凭 API 判定无 CGO 可用。第一切片必须运行实际的无 CGO 存储测试。如果验证失败，停止后续仓储迁移并提出具体 driver 调整方案，不静默更换实际驱动或引入 CGO 要求。

替代方案：直接使用默认 SQLite driver 会改变实际驱动及错误行为；另建连接池会改变 PRAGMA 与并发语义，因此均不采用。

### 2. Keep Persistence Models Separate

在 `backend/internal/infrastructure/repository/sqlite/` 内按功能建立非导出的 row 类型和显式映射。建议普通记录模型集中到 `record_models.go`，聊天/Entry/Run 模型集中到 `conversation_models.go`；文件数量以阅读便利为准，避免每个字段映射拆出独立文件。

模型覆盖 questions、question_tags、answer_attempts、mistake_reviews、attachments、conversations、messages、conversation_entry_nodes、conversation_entry_heads、agent_runs。`schema_migrations` 的模型位于 storage 包。

- 显式声明 TableName、列名和主键，使用普通字段结构，不嵌入 `gorm.Model`，不声明软删除字段。
- 时间在 row 内保持 string/可空 string，通过现有 UTC RFC3339Nano 格式化与严格解析函数映射；显式关闭 CreatedAt/UpdatedAt 自动填充，由当前业务传入的时钟决定。
- 可空外键、difficulty、Run 时间及叶子 ID 保持可空语义，不用空字符串代替 NULL。
- Entry payload 在 row 内保持 string，继续绑定为 TEXT，映射时调用既有 codec；不用自动 JSON serializer 或 `[]byte` 字段把 SQLite 存储类别变成 BLOB。
- 自定义枚举与整数在显式转换时保留现有范围校验。映射失败沿用当前错误分类，不把损坏数据变成零值成功结果。
- row 不包含 ORM 关联图；标签和其他子记录显式批量读取/写入。领域对象的 Clone 与安全投影流程保留。

替代方案：直接给领域结构添加 GORM tag 虽少一层转换，会让持久化约定影响应用/领域模型；通用反射映射又会隐藏 NULL、JSON 和错误边界，均不采用。

### 3. Make Updates and Error Mapping Explicit

创建使用 `Create`；普通读取使用 `Take`/`Find`；单字段更新和完整更新均限定字段，使用包含零值的 map 或明确 Select 的结构体更新。禁止用 `Save` 更新既有记录，避免隐式 upsert、修改主键或把未找到变成插入。

- `false`、0、空字符串以及 NULL 必须按当前仓储方法要求写入，不依赖 GORM struct Updates 跳过零值的默认行为。
- `QuestionRecord.Tags == nil` 表示不更新标签；非 nil 表示整体替换；非 nil 空切片表示清空。题目更新和标签替换仍在一个事务内。
- 普通单条未找到映射为对应仓储原有的领域 ErrNotFound；列表无数据仍成功并保留现有 API 序列化约定。
- ORM `Raw(...).Scan(...)` 不保证空结果返回 ErrRecordNotFound，必须结合 RowsAffected/结果长度显式判断；聚合返回 NULL 与完全无记录也要区分。
- 更新/删除保留原方法的 RowsAffected 检查；CAS 必须恰好影响一行。已有冲突、损坏、容量、非法输入的分类和判断优先级继续保留。
- 应用层不接收 GORM ErrRecordNotFound 作为新的公共错误，也不按数据库错误字符串判断业务状态。
- 简短 `%w` 包装保留错误链；不把完整 query、SQL 参数或整个 record 拼进新错误消息。

### 4. Express Search as Named Scopes

保留 `question.NormalizeSearchQuery`。在 `question_query.go` 中用具名函数表达文本、题型、难度、答题结果、标签、来源、归档、错因存在性及排序条件。

文本查询保留 lower + LIKE 以及 questions/answer_attempts/mistake_reviews 中现有字段范围；用 ORM 子查询表达 EXISTS，避免 JOIN 让一题多个答案/标签造成重复行及总数膨胀。标签仍为逐项 AND-of-EXISTS。`IN ?` 的值展开交给 ORM，不再手动拼占位符；空集合按当前规则跳过。

总数和分页查询从新的根 Session 各应用同一组过滤 scopes；排序、limit、offset 只作用于分页查询。不要在一个已变更的查询对象上先 Count 再复用导致条件泄漏。排序列只从既有白名单映射到 clause.Column，方向只允许 asc/desc，并保留与主排序同方向的 ID 次级排序。

页内题目加载完成后，一次性按 ID 集合查询 question_tags，并按现有顺序分组；保持无 N+1，并保证在单连接下先消费完结果再查询标签。保留当前普通搜索的读一致性级别，不额外承诺 count/page 并发快照。

允许 scopes 中出现短小且固定的 `lower(...) LIKE ?`、`EXISTS (?)` 条件片段；不以 `Raw` 包住原 Search 大段 SQL 来代替重构。替代的 JOIN+DISTINCT 方案更容易改变分页/计数与标签交集，不采用。

### 5. Use One Transaction for Each Composed Operation

最终统一业务事务入口为 `WithinTx(ctx context.Context, fn func(context.Context, *gorm.DB) error) error`，由 GORM Transaction 执行 Begin、Rollback、Commit。保留原来错误回滚、panic 回滚后继续传播，以及 COMMIT 错误返回语义。事务内核收到的是显式的同一 `*gorm.DB`，不自行 begin/commit，不从事务 context 取隐藏句柄。

- 迁移 `beginTurnTx`、`beginTurnTxWithPreflight`、`appendAssistantTextTx`、`finishAssistantTx`、`appendTx`、`appendTxWithSnapshot`、`moveHeadTx`、head/path 辅助方法、AgentRun BeginTurn 的同事务查询/插入。
- 事务中调用的方法只能使用传入 tx，禁止回到根 ORM 或底层 sql.DB；单连接下这种错误既可能死锁，也会破坏原子性。
- 公共方法只有在外层事务成功后才能返回已提交结果；callback 曾写入局部变量但 COMMIT 失败时，仍返回对应零值和错误。
- 不引入嵌套 Transaction/savepoint 充当原组合事务，不让 GORM 默认写事务隐藏新的事务边界。

为了让每个切片可编译，第一切片暂时保留旧 `WithinTx(*sql.Tx)`，新增临时的 `WithinORMTx(*gorm.DB)` 给已转换模块使用；同一个业务事务族一次性转换，不在同一操作中混用两种入口。第三切片将 ChatTurn/EntryTree/AgentRun 及组合夹具一起迁移，随后把 ORM 入口统一命名为 WithinTx，删除旧入口与临时命名。

替代方案：所有 CRUD 各自开事务无法保证 Entry、Run 和旧 Message 原子提交；让 GORM 反向取出裸 sql.Tx 会重新扩散底层 API，因此均不采用。

### 6. Preserve Tree and Run Semantics

Entry Append 保留现有准备 payload、读取 head/path、校验期望 head、工具组顺序、容量和深度、追加节点、CAS 更新 head 的顺序。节点 Create 与 head 更新共用外层事务。

CAS 使用 ORM 条件更新，包含 conversation_id、expected version、旧 active_leaf_id。叶子比较必须保留 NULL-safe 语义，例如固定条件 `active_leaf_id IS ?`；version 使用 `gorm.Expr("version + 1")`。即使写前已经检查 head，写操作仍重复携带全部 CAS 条件，检查 RowsAffected == 1，保留 ABA 防护。

MoveHead 继续只接受闭合分支点，保留空 head、缺失 head 但存在节点、跨 conversation 引用、路径损坏、容量溢出等现有处理。递归路径查询仍在读取 head 的同一事务内执行，不能拆成不一致的多次查询。

Run BeginTurn 保留以下判断顺序：会话存在性和输入校验 → 已存在 Run/旧 message pair 的复用与引用校验 → busy 判断 → head/path 与新请求校验 → 同事务写入。终态/已有请求重试优先于 busy 和当前 head；旧请求重试不回填 Run，不创建新的 user Entry。当前 Run 的 base/last/lastClosed/headVersion 校验规则与状态集合保持不变。

数据库触发器、复合外键、延迟外键、唯一键、one-active 索引继续承担最终约束；不对 Entry 使用 Save、REPLACE 或 conflict-update。未知重复 Create 必须失败，不能默默改写不可变事实。

### 7. Keep Native SQL Behind Explicit Boundaries

| 范围 | 最终处理 | 原因 |
| --- | --- | --- |
| 仓储 CRUD、批量加载、普通存在性/聚合查询 | ORM 模型、Count/Select、子查询、Updates/Delete | 去掉完整 SQL 和手工扫描 |
| 搜索函数条件、CAS、序号/文本增量表达式 | ORM clause/子查询/Expr 加固定短片段 | 保留数据库表达式和参数绑定，不拼用户输入 |
| EntryTree 递归路径 | `entry_tree_path.go` 的具名 CTE 常量，`tx.Raw(...).Scan(...)` | 路径递归及损坏检测在 SQL 中更清晰 |
| PRAGMA 与版本表初始化 DDL | storage 中集中执行 | 属于连接和 schema 配置 |
| migrations/*.sql | 原文嵌入、在迁移事务中 Exec | 保留 checksum、触发器、索引及延迟约束 |
| schema_migrations 读取与记录 | storage 内 ORM 模型/聚合查询/Create | 统一常规数据访问 |
| 测试中的 SQL | 保留必要的 fixture、故障注入、存储格式与约束断言 | 提供独立于 ORM 的验证 |

迁移管理的目标接口为 `Migrate(ctx context.Context, db *DB) error`，复用 storage.DB 的 ORM；更新 server、import-hot100 及测试调用点。版本表初始化、checksum 验证、版本连续性、未来版本拒绝、迁移应用与记录继续在一个事务里，不使用 GORM migrator/AutoMigrate。既有 `0001` 至 `0009` 文件不改字节，也不添加为 ORM 服务的新 schema migration。

不追求项目内零 SQL。验收关注普通业务方法里不再残留可替代的整段原生 CRUD、逐字段 Scan 或手工 IN 占位符；保留项必须与上表对应。

## Compatibility Acceptance

以下是本次重构的验收约束，不是新增产品要求。阶段标记对应现有能力；实施时对现有测试覆盖直接复用，对缺口补充行为测试，避免只断言 GORM 生成的 SQL 文本。

| ID / Phase | 场景与必须保持的结果 | 验证方式 |
| --- | --- | --- |
| C01 / MVP | 现有数据库经新仓储打开后，普通记录、标签、消息、可空字段与纳秒时间保持可读且可往返；payload 保持 TEXT | 以原迁移/独立 SQL 创建 fixture，新仓储回读/写入，再直接查询 typeof 与原始字段 |
| C02 / MVP | 更新 false、空字符串、0 和清空可选字段成功；不存在记录继续返回原 ErrNotFound，不插入新行 | 普通仓储 CRUD 与缺失更新/删除测试 |
| C03 / MVP | 标签 nil 不改、非 nil 替换、空切片清空；任一步失败回滚题目及标签 | 保留 TestRepositoryUpdateTags，补充组合失败场景 |
| C04 / MVP | 默认排除归档；文本命中原来所有题目/答案/复盘字段；标签取交集；来源、答题结果、HasMistakes 真/假条件一致 | 搜索测试加入组合条件、多子记录及空集合 |
| C05 / MVP | 相同排序值按 ID 同方向排序；总数不因多个关联记录增大；页边界、HasNext、空页一致 | 多关联、并列排序及分页行为测试 |
| C06 / MVP | 答案/复盘归属检查、附件元数据清理、题目/会话级联删除保持现状 | 原仓储/存储测试加数据库事实断言 |
| C07 / MVP / Phase 2 | 相同 clientMessageID 返回原消息对/Run；复用优先于 busy，旧 pair 不回填 Run；终态规则一致 | 原 chat_turn_test、chat_turn_tx_test、agent_run_store_test |
| C08 / Phase 2 | 同事务成功提交 Entry/head/Run/旧 messages/conversation 时间；任一步语句失败全部回滚 | 原组合测试与 AgentRun 分步故障注入 |
| C09 / MVP / Phase 2 | callback 成功但延迟外键在 COMMIT 失败时，公共方法返回零结果，数据库无残留；panic 回滚并传播 | 原 COMMIT 故障测试；补齐 ORM 事务 panic 场景 |
| C10 / Phase 2 | 旧 leaf/version、ABA、跨会话引用、重复 Entry、REPLACE/UPSERT 绕过均继续被拒绝；冲突不留下节点 | 原 Entry CAS、不可变、复合外键及绕过测试 |
| C11 / Phase 2 | head/path 同快照；空树、缺失 head、损坏/循环/跨会话路径、工具组次序和容量边界保持现有错误 | 原 EntryTree 路径和协议回归测试 |
| C12 / MVP / Phase 2 | migration 重复运行幂等；旧库升级保留旧 messages；checksum 不匹配、缺号或超前版本拒绝；失败不部分迁移 | 原 migration 测试，补齐版本/checksum 边界 |
| C13 / MVP / Phase 2 | 新 ORM 访问不输出 SQL 参数、消息正文及工具内容；错误链仍可识别领域错误 | 捕获 logger 的错误/慢查询路径和参数标记断言 |
| C14 / MVP / Phase 2 | 使用 modernc 同一连接；foreign_keys、busy_timeout、WAL/连接数保持配置；无 CGO 构建与真实仓储操作可用 | 文件数据库检查 PRAGMA，CGO_ENABLED=0 定向测试及全包构建 |
| C15 / Phase 2 | 当前生产仍走旧 TurnStore；ContextSummary 继续返回未实现；未交付生命周期不被隐式接入 | 装配 review、原应用测试、占位仓储断言 |
| C16 / MVP / Phase 2 | 业务查询只通过 ORM/明确原生边界；无逐字段业务扫描、手工 IN 展开、自动迁移或隐式 upsert | 源码 review 与定向 rg 清点残留并逐项解释 |

C13 限制 ORM 新增的日志/错误传播面；既有 codec 诊断回显问题仍按 proposal 的独立缺陷范围处理，不伪称已解决。

## Risks / Trade-offs

- [默认 driver 间接引入 mattn 包或 RETURNING 行为差异] → 第一切片先做无 CGO 实测、连接复用及触发器测试；失败时停在接入边界，不继续大规模替换。
- [GORM 零值、自动时间、软删除或 Save/upsert 改变事实] → 不用 gorm.Model/Save，显式更新字段、格式化时间、NULL 与 TEXT 模型，保留数据库约束测试。
- [单连接下事务内误用根 DB 导致阻塞] → 所有事务内核显式传 tx；同族一起转换，组合测试携带超时运行。
- [ORM 查询对象复用导致 Count/分页条件串扰] → 每个查询重新应用相同 scopes，覆盖多关联与页边界。
- [只验证 callback 错误遗漏 COMMIT 失败] → 保留延迟外键故障注入及零返回值/无残留断言，单独验证 panic。
- [迁移元数据重写破坏 checksum 或版本顺序] → SQL 文件保持原字节，独立测试旧库升级、版本错误及事务回滚。
- [ORM 抽象掩盖业务校验、可读性未改善] → review 仓储主流程及搜索函数；不接受用 Raw 重包现有 CRUD，或建立需要跨层追踪的通用仓储框架。
- [与尚未提交的会话树切片交叉] → 以实施开始时的工作区快照为基线，保留原能力及任务状态；若原切片继续演进，只迁移新出现的实际存储操作，不擅自实现后续流程。

## Migration Plan

### Stage 1: Storage Foundation

锁定依赖，验证 official dialector + modernc 同连接、无 CGO 及基础读写。增加 ORM 配置与临时事务入口，迁移 schema_migrations 元数据访问并更新启动/导入/测试调用点；旧仓储暂时沿用原事务入口，避免无行为收益的全量机械改动。此阶段单独 review 驱动兼容、日志、迁移失败语义。

### Stage 2: Ordinary Repositories

建立普通记录 row 模型和映射，迁移题目/标签、答案、复盘、附件、Conversation/Message 的普通 CRUD，拆分题目搜索 scopes。多语句方法使用 ORM 外层事务。旧 ChatTurn 的必要扫描辅助暂时保留到第三阶段；普通仓储定向测试及旧库往返通过后 review。

### Stage 3: Chat, Tree and Run Transactions

一次性迁移 ChatTurn、EntryTree、AgentRun 当前切片的事务内核、相关 row 映射、递归 CTE 结果映射与全部依赖旧内核签名的单族/组合测试夹具。事务内核签名与测试互相耦合，这一批次完成所有夹具适配后统一编译和验证，不把中间文件转换视为独立可编译切片。保留业务顺序、head CAS、引用校验、不可变/闭合和零结果要求。统一 WithinTx 为 ORM 入口并删除旧事务入口及只服务旧实现的扫描辅助。定向事务/并发/COMMIT 故障测试通过后 review。

### Stage 4: Final Verification

核对 C01–C16，清点全部残留 SQL、直接数据库访问与依赖，验证 migration 文件字节未变且原功能边界不变。执行后端全量 `go test -count=1 ./...`、`go vet ./...`、`CGO_ENABLED=0 go build ./...`；第一阶段的无 CGO 定向测试只有在后续修改相关配置/驱动时才需重跑。无需启动前端或做页面验证。

### Deployment and Rollback

本变更不引入 schema/数据格式升级。已有库按现有版本机制打开；目标程序和迁移前基线程序应能读取对方创建的同格式记录。每阶段保持可编译并 review 后再继续。

回退采用回退本次 GORM 代码与依赖的提交，保留已有 SQL migration 和数据库文件；不对已有数据库执行反向 schema 或格式迁移。回退目标必须支持该库已经存在的 migration 版本，尤其当前工作区的 0009；不承诺让仅支持 0008 的旧程序读取新版数据库。

## Implementation Evidence

### Stage 1

用户已于方案 review 后确认开始执行。存储接入锁定 `gorm.io/gorm v1.31.2`、`gorm.io/driver/sqlite v1.6.0`，继续使用 `modernc.org/sqlite v1.58.0` 的原连接。新增 `ORM(ctx)` 和临时 `WithinORMTx`，旧仓储继续使用裸 SQL 事务入口。`Migrate(ctx, *DB)` 的元数据访问采用 ORM，DDL 和嵌入迁移原文保留。

- `orm_test.go` 覆盖连接身份、modernc 驱动、单连接等待/context、文件库 PRAGMA、零值/NULL/RETURNING/触发器、callback/panic/延迟外键 COMMIT 回滚和生产 Silent logger。
- `migrate_test.go` 覆盖 checksum、已应用版本缺失、超前版本、应用序列缺号、SQL/元数据/COMMIT 失败，检查空库及已有库完整回滚；既有 v8→v9/幂等测试通过。
- `CGO_ENABLED=0 go test -count=1 ./internal/infrastructure/storage ./cmd/server`、`CGO_ENABLED=0 go build ./...`、`go test -run '^$' ./...` 通过，全部调用点适配并可编译。
- 原有 tracked migration 无内容 diff，第九个未提交 migration 未编辑。独立只读 review 未发现阻塞；modernc 的 COMMIT 失败主动回滚及后续连接可用性由 fixture 验证。

### Stage 2

Question/Answer/Review/Attachment/Conversation/Message 常规仓储已改用 ORM，非导出 row 明确声明现有表/列和时间字符串。更新使用 map 包含零值和 NULL。标签 nil 保留、非 nil 替换/清空，多语句写删除使用同一临时 ORM tx；既有 ChatTurn 扫描辅助保留到阶段 3。

- `question_query.go` 的具名 scopes 使用固定 lower/LIKE、关联条件、EXISTS/NOT EXISTS 和 ORM `IN ?`；count 与分页从独立 session 构建、排序列白名单、同方向 ID 次级排序、页内标签批量加载。
- `sql_test/question_orm_test.go`、`record_orm_test.go`、`conversation_orm_test.go` 用独立 SQL fixture 覆盖原格式往返、零值/NULL/缺失/损坏时间、11 个文本搜索字段、组合过滤/多关联去重/count/并列排序/空页、标签跨批和附件/touch/COMMIT 故障回滚及数据库级联。
- `go test -count=1 -timeout 120s ./internal/infrastructure/repository/sqlite/sql_test ./internal/infrastructure/repository/sqlite` 通过；各族无 CGO 定向测试与 vet 通过。独立静态 review 未发现已确认的回归；主会话对照原 CRUD/搜索 diff 验证业务顺序及条件。
- 本阶段生产代码无完整原生 CRUD、手工 IN 展开、逐字段扫描或 Save；保留的固定 SQL 条件表达式归入原生 SQL 边界。

### Stage 3

ChatTurn、EntryTree、AgentRun 当前切片已协调迁移为 `*gorm.DB` 事务内核；正式入口统一为 `WithinTx(ctx, func(context.Context, *gorm.DB) error)`。已删除临时入口与旧逐字段扫描/列清单辅助，事务内核仅使用传入 tx，统一事务入口之外无自行开启的嵌套 Transaction。

- Run/Entry/Head row 使用显式列名、string 时间与可空指针；Entry payload 仍为 TEXT，原 codec 和协议校验保持。`entry_orm_test.go`、`agent_run_orm_test.go` 使用独立 SQL 历史 fixture 核对读写、NULL、时区/纳秒、损坏行及缺失递归查询结果。
- head CAS 同时限定 conversation/version/NULL-safe leaf，通过 `gorm.Expr("version + 1")` 递增且要求 `RowsAffected == 1`。`entryPathSQL` 保留原递归语句、maxEntries + 1 防护和路径验证顺序；`Raw.Scan` 明确判断空结果。
- 原 ChatTurn/AgentRun 单族与 Entry+Chat 组合故障测试适配同一 ORM tx，保留直接 SQL 约束/故障注入和数据库事实断言；retry 优先于 busy/current head，legacy pair 不回填 Run。公共方法在 callback/COMMIT 失败时返回零结果。
- 机械命名收尾后执行 `go test -count=1 -timeout 120s ./internal/infrastructure/storage ./internal/infrastructure/repository/sqlite ./internal/infrastructure/repository/sqlite/sql_test ./internal/application/...` 和 `go build ./...`，全部通过。树、聊天/Run 两组独立只读 review 均未发现确认的迁移回归。
- 生产 `cmd/server/main.go` 仍装配 `NewChatTurnRepository`；`TestContextSummaryRemainsUnimplemented` 与原应用测试通过，未接入后续生命周期能力。

### Stage 4

最终验收全部完成。普通仓储/storage 的独立 review 对照 HEAD 原实现和当前代码，未发现确认的语义回归；主会话复核代码边界、调用点和测试结果。

| 验收 | 已验证证据 |
| --- | --- |
| C01 | question/record/conversation/entry/agent_run ORM 独立 SQL fixture 与往返测试；payload typeof='text'、时间与 NULL 原始字段断言 |
| C02 | question/record/conversation ORM 零值、NULL、缺失更新/删除不创建行及损坏时间测试 |
| C03 | 原标签测试与 `TestQuestionORMTagFailureRollsBackWholeUpdate`；跨批标签失败整体回滚 |
| C04 | `TestQuestionORMSearchAllTextFields` 和组合过滤测试，覆盖 11 个文本字段、标签交集、来源、结果、归档和 HasMistakes |
| C05 | question ORM 组合分页测试：多关联 count 去重、并列排序、页边界和空页 |
| C06 | record owner/error priority、附件 cleanup rollback、question/conversation cascade 测试 |
| C07 | 原 chat_turn/agent_run 重试与终态测试；独立静态 review 核对先复用、后 busy 及 legacy 不回填 |
| C08 | chat_turn_tx/entry_chat_tx/agent_run_store 各写点故障注入，直接查询多表确认全提交或全回滚 |
| C09 | ORM callback/panic/延迟外键 COMMIT 测试及公共方法 COMMIT 失败零结果断言，连接后续可用 |
| C10 | 原 Entry CAS/ABA、跨会话、不可变与 REPLACE/UPSERT 绕过测试；树 review 核对 CAS 条件及单行更新 |
| C11 | 原路径/协议/容量测试及 `TestEntryORMRejectsMissingAndCorruptRows`；head/path 同事务源码 review |
| C12 | 原迁移幂等/v8→v9 与新增 migrate 版本/checksum/故障回滚测试；tracked migration 无 diff |
| C13 | `TestORMSilentLoggerDoesNotFormatSQLOnErrorOrSlowQuery`；错误链测试及显式领域错误映射 review |
| C14 | modernc 同连接/PRAGMA/单连接等待测试；Stage 1 无 CGO 实际存储及 server 测试，最终无 CGO 全包构建 |
| C15 | server 仍使用旧 TurnStore 装配；ContextSummary 未实现断言及应用测试 |
| C16 | 全部非测试数据库调用清点；无临时入口、业务 sql.Tx、Save、AutoMigrate、逐字段业务扫描或手工 IN 展开；领域/应用无 GORM 依赖 |

最终检查从 `backend/` 执行并全部通过：`go test -count=1 ./...`、`go vet ./...`、`CGO_ENABLED=0 go build ./...`。阶段 1 后驱动和连接配置未改变，无需重复已通过的无 CGO 定向存储测试。`git diff --check` 通过，仅存在 Git 的 LF/CRLF 提示。

保留的非测试原生 SQL/表达式如下，均处于方案原定边界：

| 位置 | 保留内容与原因 |
| --- | --- |
| `storage/sqlite.go` | 同一连接初始化的 foreign_keys/busy_timeout/journal_mode 三个 PRAGMA；数据库引擎配置 |
| `storage/migrate.go` | schema_migrations DDL 与嵌入 migration 原文；维持现有 schema/checksum 和触发器约束 |
| `entry_tree_path.go` | 唯一 `Raw` 查询 `entryPathSQL`；递归 owner/path/depth/capacity 语义 |
| `question_query.go` | 固定 lower/LIKE、EXISTS/NOT EXISTS 和列关联表达式；其余构造/参数、IN、分页由 ORM 完成 |
| migration/chat/tree ORM 查询 | `COALESCE(MAX(...), ...)` 聚合、version/文本更新表达式和 CAS 的 `IS ?`；由 Model/Select/Scan/Updates 执行，Scan 无逐字段顺序耦合 |

既有 tracked SQL migrations 未修改，原有未提交的 0009 migration 未编辑；原 dirty 会话树/AgentRun 工作与阶段边界保留。本次不变更 schema/数据库格式。此前发现的 codec 非法 UTF-8 往返不一致及诊断回显未知/重复 key 问题仍属于独立待修缺陷，本次未修复。回退仍须使用支持现有 migration 版本（含 0009）的程序。

## Open Questions

无阻塞待定项。Stage 1 已通过实际测试确认 GORM v1.31.2、官方 SQLite dialector v1.6.0 与 modernc v1.58.0 同连接及无 CGO 可用性。用户已确认实施，本次四阶段任务已完成；是否归档本 OpenSpec change 留待后续指令。
