# Proposal

## Why

当前 Go 后端的 SQLite 仓储混合了业务判断、SQL 字符串拼接、参数占位符和逐字段扫描；题目搜索以及聊天、会话树事务尤其难以阅读。采用统一的数据映射和查询方式，降低理解与维护成本，同时保持已有数据、接口和事务行为兼容。

## What Changes

- [MVP] 普通业务仓储统一使用 GORM：题目、标签、答案、复盘、附件、Conversation 和 Message 的查询、创建、更新、删除及模型映射。
- [MVP] 题目搜索拆成可组合的具名筛选函数，统一总数与分页条件，保留现有搜索范围、标签交集、排序和归档规则。
- [MVP / Phase 2] 存储层复用现有 modernc SQLite 连接，集中管理 ORM 配置、上下文、事务、领域错误转换及显式字段更新。
- [Phase 2] 将当前已实现的 ChatTurn、EntryTree、AgentRun BeginTurn 事务内核一起迁移到 GORM，保留 CAS、幂等重试、引用校验、不可变事实和提交失败语义。
- [MVP / Phase 2] 递归 CTE、版本迁移脚本、触发器及 PRAGMA 保留显式 SQL；业务 CRUD 与迁移元数据读写使用 ORM，剩余原生 SQL 集中在用途明确的边界内。
- [MVP / Phase 2] 建立明确的存储兼容性验收标准与有针对性的回归覆盖，验证旧数据库回读、零值更新、原子提交、约束及无 CGO 构建。

### Non-goals

- 不改变 HTTP/SSE 协议、领域模型、应用层仓储端口和用户可见业务规则。
- 不修改现有 migration 文件内容，不启用 AutoMigrate，不引入软删除或隐式关联写入。
- 不完成会话树原变更中尚未实现的 Run 生命周期、Recorder、Recovery、Projection、Compaction 或生产装配切换；ContextSummary 仓储仍保持未实现状态。
- 不混入前次 review 发现的 UTF-8 编码漂移和 JSON 诊断回显修复；这些问题单独处理。
- 不调整前端，不扩展数据库产品支持，不引入通用仓储框架或性能重构。

## Capabilities

### New Capabilities

无。本次是保持行为兼容的基础设施重构；按 OpenSpec 纯重构规则在 `.openspec.yaml` 设置 `skip_specs: true`，不为 GORM 实现细节新建产品 capability。原计划 specs 承载的兼容性要求与验收矩阵集中写入 design、tasks，供 review。

### Modified Capabilities

无。已有业务及会话树要求继续生效；本次仅更换存储实现，不修改原会话树变更的阶段边界。

## Impact

- `backend/internal/infrastructure/storage/`：连接初始化、事务管理、迁移元数据访问和对应测试；现有 `migrations/*.sql` 原文保留。
- `backend/internal/infrastructure/repository/sqlite/`：全部已实现仓储及事务内核，新增仅供基础设施使用的持久化模型与映射；ContextSummary 占位仓储不扩展功能。
- `backend/cmd/server/main.go`、`backend/cmd/import-hot100/main.go` 以及使用存储初始化/迁移接口的测试：适配基础设施内部接口。领域及应用层端口保持兼容。
- `backend/go.mod`、`backend/go.sum`：引入 GORM 及 SQLite dialector；保持 modernc 作为实际数据库连接驱动，具体版本在实施时锁定并验证。
- 仓储、存储、聊天及会话树现有回归测试：继续作为行为基线，调整事务夹具并补充 ORM 特有边界测试。
- 迁移范围包含当前工作区中已存在但尚未提交的 AgentRun schema/BeginTurn 与事务组合代码；不覆盖这些工作的业务语义，也不把后续阶段任务视为本次交付。

本轮交付仅为 proposal、design、tasks 及纯重构的 skip_specs 标记，实施需在文档 review 后另行确认。
