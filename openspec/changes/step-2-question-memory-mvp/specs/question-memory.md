# Question Memory

## ADDED Requirements

### Requirement: 题目和作答 API
[MVP] 系统 MUST 提供题目、作答和复盘的 REST API，支持创建、查询、更新、归档、恢复和永久删除；请求失败时 MUST 返回稳定错误码和 requestId。

#### Scenario: 缺少必填字段
- **WHEN** 创建题目时标题或 Markdown 题干为空
- **THEN** API 返回 `400 invalid_request`，不创建任何业务记录

#### Scenario: 永久删除题目
- **WHEN** 用户确认永久删除已有题目
- **THEN** API 在一个事务中删除其作答、复盘、标签和无其他引用的附件元数据

### Requirement: 搜索筛选和分页
[MVP] 系统 MUST 支持关键词、题型、难度、结果、标签、来源、归档状态和是否错题的组合筛选，并返回稳定 offset 分页结果。

#### Scenario: 组合筛选
- **WHEN** 用户同时指定关键词、题型、难度和错误结果
- **THEN** 返回结果 MUST 同时满足全部条件，并包含总数和是否有下一页

#### Scenario: 分页越界
- **WHEN** page 小于 1 或 pageSize 超过 100
- **THEN** API 返回 `400 invalid_request`，不执行查询

### Requirement: 附件元数据
[MVP] 系统 MUST 为受支持的本地图片保存可迁移附件引用和元数据；附件缺失时题目详情仍 MUST 可读取。

#### Scenario: 附件文件缺失
- **WHEN** 题目引用的托管图片不存在
- **THEN** 题目详情正常返回，并将附件标记为不可用
