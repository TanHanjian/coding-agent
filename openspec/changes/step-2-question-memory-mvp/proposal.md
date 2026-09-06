## Why

Step 1 已完成 Go 服务、SQLite、事务和 HTTP 基础设施。本变更为题库、作答、复盘和附件能力建立可评审、可替换的领域边界，供后续逐步实现。

## What Changes

- [MVP] 定义题目、题型模板、多次作答、错因复盘、标签和附件元数据模型。
- [MVP] 定义题目生命周期、搜索筛选、排序分页和题目详情聚合接口。
- [MVP] 定义 Go repository、service 和 chi handler 的函数骨架。
- [MVP] 定义 SQLite 业务表迁移和外键约束。

### Non-goals

- 不实现题目、作答、复盘或附件的核心读写逻辑。
- 不接入 Eino、模型调用、向量索引或 FSRS。
- 不实现用户认证、会话、备份恢复或 React 业务页面。

## Impact

- 新增 `backend/internal/question` 领域边界和 `0002_question_memory.sql` 迁移。
- 后续 Step 2 实现必须遵循本变更的 API、事务和错误边界。
