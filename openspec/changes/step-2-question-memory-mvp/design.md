# Design

## 架构

React 通过 chi 注册的 REST API 访问 Go service。service 负责输入校验、领域规则和事务边界；repository 只负责 SQLite 映射。附件二进制由文件存储管理，数据库只保存附件元数据。

## 模块边界

- `question/model.go`：领域枚举、实体、输入和搜索类型。
- `question/repository.go`：题目、作答、复盘和附件 repository 接口。
- `question/service.go`：业务 service 接口和未实现骨架。
- `question/validator.go`：字段、枚举和分页校验入口。
- `question/http.go`：chi 路由注册和 handler 骨架。
- `question/errors.go`：题库领域错误到 HTTP 错误码的映射入口。
- `storage/migrations/0002_question_memory.sql`：业务表和外键约束。

## 事务边界

创建或更新题目及标签、永久删除题目及关联记录、附件元数据提交均必须由 service 通过 Step 1 的 `TxManager` 执行。repository 不持有跨请求事务，也不负责文件复制。

## 搜索分页

Step 2 使用 offset 分页，默认第 1 页、每页 20 条，最大 100 条；默认按 `updated_at DESC, id DESC` 稳定排序。筛选条件组合使用 AND，标签筛选要求包含全部指定标签。全文索引延后至 Step 5。

## 附件生命周期

附件上传先写临时文件，完成校验后原子移动并提交元数据。缺失附件不阻塞题目详情；删除前由 service 检查引用关系。

## 错误边界

handler 将输入错误映射为 `invalid_request`，不存在记录映射为 `not_found`，非法状态映射为领域冲突错误，其余错误沿用 Step 1 的 `code + message + requestId` 协议。
