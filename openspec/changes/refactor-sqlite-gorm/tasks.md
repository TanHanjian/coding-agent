# Tasks

本任务单已获用户确认并完成四阶段实施；各阶段完成检查与 review 后推进，验收编号对应 [design.md](design.md) 的 C01–C16，执行证据见该文档 Implementation Evidence。所有 Go 命令从 `backend/` 执行。`specs` 按纯重构规则跳过，既有业务/会话树规范继续适用。

## 1. Storage Foundation

- [x] 1.1 [MVP / Phase 2] 在 `go.mod/go.sum` 锁定 GORM 与官方 SQLite dialector，沿用 modernc 实际驱动；在 `storage.DB` 增加命名 ORM 成员与绑定 context 的访问入口，保留底层 sql.DB、连接池配置、PRAGMA 和 Close/Ping；验证初始化失败清理与文件库 foreign_keys/busy_timeout/WAL 配置，覆盖 C14。
- [x] 1.2 [MVP / Phase 2] 设置 Silent logger、关闭默认写事务并明确不开启自动迁移/隐式错误翻译，建立临时 `WithinORMTx`，暂时保留旧 WithinTx；增加 ORM 成功提交、callback 失败回滚、panic 回滚并传播测试，以及错误/慢查询参数不输出测试，覆盖 C09、C13。
- [x] 1.3 [MVP / Phase 2] 用现有连接验证 ORM Create/Take/Updates、RETURNING、事务与数据库触发器协作；执行 `CGO_ENABLED=0 go test -count=1 ./internal/infrastructure/storage` 与 `CGO_ENABLED=0 go build ./...`，确认无 CGO 下实际数据库操作正常。若失败，记录具体错误并停止后续仓储迁移，先 review driver 方案，覆盖 C14。
- [x] 1.4 [MVP / Phase 2] 在 storage 包建立 schema_migrations row，迁移常规版本查询与记录为 ORM；改为 `Migrate(ctx, *storage.DB)`，保留版本表 DDL、SQL 脚本、checksum/连续性/超前版本拒绝及单事务执行；适配 server、import-hot100 和全部测试调用点，验证全包可编译且现有迁移测试通过。
- [x] 1.5 [MVP / Phase 2] 补齐迁移幂等、checksum 不匹配、缺号、数据库超前版本及中途失败整体回滚测试，复用旧库 v8 到 v9 升级保留 messages 测试；核对现有 migration 文件原始字节未变，覆盖 C12。配置/驱动改动涉及无 CGO 时重跑 1.3 的定向检查。
- [x] 1.6 [MVP / Phase 2] Review 本阶段连接复用、日志、迁移事务和临时双入口边界；执行 `go test -count=1 ./internal/infrastructure/storage ./cmd/server`，记录已验证 driver 版本、结果与约定到 design，确认本阶段可编译、旧仓储仍正常使用原入口后进入阶段 2。

## 2. Ordinary Repositories

- [x] 2.1 [MVP] 建立普通记录 row 与显式领域映射，声明现有 TableName/列名/主键，保留 UTC RFC3339Nano 文本、NULL、调用方时间及空列表表现；不使用 gorm.Model/软删除/自动关联图。用原迁移和独立 SQL 生成旧格式 fixture，验证映射回读及新写入能被原格式读取，覆盖 C01。
- [x] 2.2 [MVP] 迁移 Question Create/Exists/Get/Update/SetArchived/DeletePermanent 与标签加载/替换；用包含零值的显式更新，保持 Tags=nil/替换/清空及原 ErrNotFound，采用 ORM 外层事务；保留标签用例并增加未找到不插入、false/空字符串/NULL 更新和标签写失败回滚测试，覆盖 C02、C03。
- [x] 2.3 [MVP] 在 `question_query.go` 实现具名搜索 scopes，转换文本、IN 集合、标签 AND-of-EXISTS、source、Archived、结果与 HasMistakes 条件；从独立查询对象应用相同过滤完成 Count 与分页，白名单排序并保留 ID 次级排序，批量加载标签；补充多关联记录、组合过滤、并列排序、空集合、空页和 HasNext 测试，覆盖 C04、C05。
- [x] 2.4 [MVP] 迁移 Answer 和 Review CRUD/列表/归属校验、删除时附件元数据清理，用 ORM 事务保留多语句原子性；验证现有 CRUD、错误归属和清理测试，补齐零值/NULL/缺失记录与清理失败回滚，覆盖 C02、C06。
- [x] 2.5 [MVP] 迁移 Attachment CRUD/owner 列表以及 Conversation/Message 普通 CRUD，保留时间、消息顺序、会话 updated_at 和数据库级联行为；验证相关旧用例及缺失更新/删除、附件元数据/会话删除结果，覆盖 C01、C02、C06。暂留旧 ChatTurn 所需扫描辅助到阶段 3。
- [x] 2.6 [MVP] Review 普通仓储主流程、row 映射和搜索 scopes，确认无逐字段业务扫描/手工 IN 展开/Save upsert，相关行为测试和旧库 fixture 已随本阶段交付；执行 `go test -count=1 ./internal/infrastructure/repository/sqlite/sql_test`，记录兼容性覆盖与必要短 SQL 条件片段到 design 后进入阶段 3。

## 3. Chat, Tree and Run Transactions

3.2–3.5 属于同一个协调迁移批次：事务内核签名和依赖它们的夹具互相耦合，不把中间任务当作独立可编译切片，也不提前设置运行测试关口。各任务先核对代码与用例，3.5 完成全部夹具适配后统一编译并运行本批次测试。

- [x] 3.1 [MVP / Phase 2] 建立 Message/Entry/Head/Run row 与映射，保留 NULL leaf/parent/Run 时间、数值范围、Entry payload TEXT 和原 codec/Clone/安全投影；用独立 SQL 的历史 fixture 验证回读及新写入 typeof(payload_json)='text'，映射损坏数据仍返回原错误，覆盖 C01、C11。
- [x] 3.2 [MVP / Phase 2] 迁移 ChatTurn 公共方法与 beginTurnTx/preflight/text/finish 内核，显式传同一个 ORM tx，保留成对批量创建、历史过滤、busy/重试优先级、增量文本、终态规则及原消息时钟；核对原 chat_turn_test、chat_turn_tx_test 中业务/语句失败/COMMIT 失败用例与新内核对应，并在 3.5 全部夹具适配后统一运行验证，覆盖 C07、C09。
- [x] 3.3 [Phase 2] 迁移 EntryTree GetHead/Get/ListChildren/ReadPath/Append/MoveHead 和 head/path 内核；常规 CRUD/聚合用 ORM，递归 CTE 用具名查询与结果模型；CAS 携带 conversation+leaf NULL-safe 条件+version，并要求 RowsAffected==1，保留闭合/容量/历史校验。核对原 EntryTree 空树、损坏路径、工具组、分页、CAS/ABA、跨会话、不可变与 REPLACE 绕过用例与新实现对应，在 3.5 统一运行验证，覆盖 C10、C11。
- [x] 3.4 [Phase 2] 迁移 AgentRun 当前 BeginTurn、Run 查找/创建与引用一致性校验，沿用 preflight/snapshot 优化及原校验顺序，保证已有/终态/旧 pair 重试先于 busy 和当前 head；核对原 AgentRun 重试、无回填、并发 client、会话隔离、rewind/version 及各写点故障回滚用例与新实现对应，在 3.5 统一运行验证，覆盖 C07、C08、C10。
- [x] 3.5 [MVP / Phase 2] 将所有依赖已改内核签名的单族、组合事务和故障注入夹具一起改用同一 ORM tx，保留直接 SQL 的独立约束/故障断言；验证 Entry/head/Run/messages/conversation 时间全部提交或回滚，特别是 callback 成功但延迟外键 COMMIT 失败后的零结果和无残留，覆盖 C08、C09。执行 `go test -count=1 -timeout 120s ./internal/infrastructure/repository/sqlite ./internal/infrastructure/repository/sqlite/sql_test`。
- [x] 3.6 [MVP / Phase 2] 将 ORM WithinORMTx 统一命名为 WithinTx，迁移全部调用点并删除旧裸 sql.Tx 入口及不再使用的扫描/列清单辅助；核对事务内部不访问根 DB、不开启嵌套 Transaction。补齐必要 ORM事务 panic 与缺失 Raw Scan 结果测试，覆盖 C09、C11、C16，验证存储/仓储定向测试通过且 `go build ./...` 成功。
- [x] 3.7 [Phase 2] Review 当前生产 TurnStore 装配、ContextSummary 未实现和原会话树阶段边界，确认未隐式接入生命周期/Recorder/Recovery/Projection/Compaction；验证原应用测试与 ContextSummary 未实现返回值，覆盖 C15。将本阶段事务与原生 CTE 约定/结果记录到 design 后进入阶段 4。

## 4. Final Verification

- [x] 4.1 [MVP / Phase 2] 对照 design 的 C01–C16 逐项核对已有测试/新增测试/源码 review 证据，确认每项有覆盖；清点 backend 非测试 SQL 调用，逐项对应原生 SQL 边界表，确认普通 CRUD 不以 Raw 重包旧 SQL、无手工 IN 占位符和重复扫描，覆盖 C16。
- [x] 4.2 [MVP / Phase 2] 执行 `go test -count=1 ./...`、`go vet ./...`、`CGO_ENABLED=0 go build ./...`；若驱动/连接配置在阶段 1 后变化，重跑无 CGO 定向存储测试。记录命令与结果，所有必需检查通过；无需启动前端。
- [x] 4.3 [MVP / Phase 2] 最终 review 基础设施边界、领域/应用端口兼容、数据库格式与原 migration 字节、错误链、日志和原有未提交工作；确认原 codec 两个独立缺陷未被误报为本次已修复，回退方案针对支持现有数据库版本的程序。交付变更说明及剩余原生 SQL 清单，完成本次重构的最终验收。
