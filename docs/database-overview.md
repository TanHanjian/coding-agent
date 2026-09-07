# 数据库梳理文档

> 本文档基于当前数据库迁移脚本与 Go 领域模型整理，反映仓库现状，不额外假设尚未实现的业务能力。

## 1. 概览

当前项目使用 SQLite，数据库围绕“面试题记忆”场景设计，以 `questions` 为核心实体，向外关联标签、作答记录、错题复盘和附件。

```text
                         ┌──────────────────┐
                         │    questions     │
                         │    面试题主表     │
                         └───────┬──────────┘
             ┌──────────────────┼──────────────────┐
             │                  │                  │
             │                  │                  │
   ┌─────────▼────────┐ ┌───────▼──────────┐ ┌──────▼──────────┐
   │  question_tags    │ │  answer_attempts │ │   attachments    │
   │    问题标签       │ │     作答记录      │ │      附件         │
   └──────────────────┘ └────────┬─────────┘ └──────────────────┘
                                  │ 可选关联
                         ┌────────▼─────────┐
                         │   mistake_reviews│
                         │     错题复盘      │
                         └──────────────────┘
```

基础设施表 `schema_migrations` 独立存在，用于记录迁移执行状态，不属于业务实体关系。

## 2. 数据库迁移

| 迁移 | 内容 |
|---|---|
| `0001_init.sql` | 创建 `schema_migrations` |
| `0002_question_memory.sql` | 创建问题、标签、作答、复盘和附件表，以及查询索引 |

迁移记录字段：

- `version INTEGER PRIMARY KEY`：迁移版本号。
- `applied_at TEXT NOT NULL`：应用时间。
- `checksum TEXT NOT NULL`：迁移内容校验值。

## 3. 业务实体

### 3.1 `questions`：面试题

系统的核心表，每条记录代表一道面试题。

| 字段 | 类型 | 约束/默认值 | 说明 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY`, `NOT NULL` | 问题唯一标识 |
| `title` | `TEXT` | `NOT NULL` | 问题标题 |
| `type` | `TEXT` | `NOT NULL`; 枚举约束 | 问题类型 |
| `body_markdown` | `TEXT` | `NOT NULL` | Markdown 格式的问题正文 |
| `difficulty` | `TEXT` | 可空；枚举约束 | 难度，可为空 |
| `source_name` | `TEXT` | `NOT NULL DEFAULT ''` | 来源名称 |
| `source_url` | `TEXT` | `NOT NULL DEFAULT ''` | 来源地址 |
| `is_archived` | `INTEGER` | `NOT NULL DEFAULT 0`; 只能为 `0/1` | 是否归档 |
| `created_at` | `TEXT` | `NOT NULL` | 创建时间 |
| `updated_at` | `TEXT` | `NOT NULL` | 最后更新时间 |

`type` 允许值：

- `algorithm`：算法题
- `knowledge`：知识题
- `system_design`：系统设计题
- `behavioral`：行为题
- `other`：其他

`difficulty` 允许值：`easy`、`medium`、`hard`，也可以为 `NULL`。

#### 业务语义

- 归档通过 `is_archived = 1` 表示，当前设计没有物理删除标记之外的归档时间或归档原因。
- `source_name` 与 `source_url` 不允许为 `NULL`，未提供时使用空字符串。
- 标签不直接存放在 `questions` 的 JSON 或逗号分隔字段中，而是通过关联表规范化存储。

### 3.2 `question_tags`：问题标签

问题与标签之间的关联表。

| 字段 | 类型 | 约束/默认值 | 说明 |
|---|---|---|---|
| `question_id` | `TEXT` | `NOT NULL`, 外键 | 所属问题 |
| `tag` | `TEXT` | `NOT NULL` | 标签文本 |

主键为复合主键：`(question_id, tag)`。

这意味着：

- 一个问题可以有多个标签。
- 同一个标签可以用于多个问题。
- 同一个问题不能重复出现相同标签。
- 当前没有独立的标签实体表，因此标签本身没有全局 ID、名称表或元数据。

### 3.3 `answer_attempts`：作答记录

记录用户针对某道题的一次作答尝试。

| 字段 | 类型 | 约束/默认值 | 说明 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY`, `NOT NULL` | 作答唯一标识 |
| `question_id` | `TEXT` | `NOT NULL`, 外键 | 所属问题 |
| `body_markdown` | `TEXT` | `NOT NULL DEFAULT ''` | 文字答案 |
| `code` | `TEXT` | `NOT NULL DEFAULT ''` | 提交的代码 |
| `code_language` | `TEXT` | `NOT NULL DEFAULT ''` | 代码语言 |
| `result` | `TEXT` | `NOT NULL`; 枚举约束 | 作答结果 |
| `duration_ms` | `INTEGER` | 可空 | 作答耗时，单位毫秒 |
| `created_at` | `TEXT` | `NOT NULL` | 创建时间 |
| `updated_at` | `TEXT` | `NOT NULL` | 最后更新时间 |

`result` 允许值：

- `skipped`：跳过
- `incorrect`：错误
- `partial`：部分正确
- `correct`：正确

#### 关系与语义

- `questions 1:N answer_attempts`。
- 删除问题时，相关作答记录级联删除。
- `duration_ms` 可为空，表示未记录耗时；数据库只保证整数类型，没有检查非负值。

### 3.4 `mistake_reviews`：错题复盘

记录问题的复盘内容，也可以选择性地指向某次具体作答。

| 字段 | 类型 | 约束/默认值 | 说明 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY`, `NOT NULL` | 复盘唯一标识 |
| `question_id` | `TEXT` | `NOT NULL`, 外键 | 所属问题 |
| `answer_attempt_id` | `TEXT` | 可空，外键 | 关联的作答记录 |
| `mistake_category` | `TEXT` | `NOT NULL DEFAULT ''` | 错误分类 |
| `review_markdown` | `TEXT` | `NOT NULL DEFAULT ''` | 复盘正文 |
| `correction_markdown` | `TEXT` | `NOT NULL DEFAULT ''` | 修正内容 |
| `key_conclusions` | `TEXT` | `NOT NULL DEFAULT ''` | 关键结论 |
| `ai_content_markdown` | `TEXT` | `NOT NULL DEFAULT ''` | AI 生成内容 |
| `ai_source` | `TEXT` | `NOT NULL DEFAULT ''` | AI 内容来源 |
| `created_at` | `TEXT` | `NOT NULL` | 创建时间 |
| `updated_at` | `TEXT` | `NOT NULL` | 最后更新时间 |

#### 关系与删除行为

- `questions 1:N mistake_reviews`，每条复盘必须属于一个问题。
- `answer_attempts 1:N mistake_reviews` 是可选关系，因为 `answer_attempt_id` 可以为空。
- 删除问题时，复盘级联删除。
- 删除作答记录时，复盘保留，`answer_attempt_id` 置为 `NULL`（`ON DELETE SET NULL`）。

当前数据库没有约束 `mistake_reviews.question_id` 必须与关联作答的 `answer_attempts.question_id` 相同。该一致性需要由应用层保证。

### 3.5 `attachments`：附件

保存问题相关文件的元数据，文件内容本身预计由文件系统存储层管理。

| 字段 | 类型 | 约束/默认值 | 说明 |
|---|---|---|---|
| `id` | `TEXT` | `PRIMARY KEY`, `NOT NULL` | 附件唯一标识 |
| `question_id` | `TEXT` | `NOT NULL`, 外键 | 所属问题 |
| `owner_type` | `TEXT` | `NOT NULL` | 业务拥有者类型 |
| `owner_id` | `TEXT` | `NOT NULL` | 业务拥有者 ID |
| `original_name` | `TEXT` | `NOT NULL` | 原始文件名 |
| `stored_name` | `TEXT` | `NOT NULL UNIQUE` | 实际存储文件名 |
| `mime_type` | `TEXT` | `NOT NULL` | MIME 类型 |
| `size_bytes` | `INTEGER` | `NOT NULL`; `>= 0` | 文件大小 |
| `sha256` | `TEXT` | `NOT NULL` | 文件 SHA-256 摘要 |
| `created_at` | `TEXT` | `NOT NULL` | 上传时间 |

#### 归属模型

附件同时保存两种归属信息：

1. `question_id`：数据库级别明确约束的所属问题。
2. `owner_type + owner_id`：应用层多态归属，用于指向问题、作答或复盘等对象。

目前只有 `question_id` 是真正的外键。`owner_id` 没有数据库外键，因此：

- 数据库不会验证 `owner_id` 对应的对象是否存在。
- 数据库不会自动保证 `owner_type` 与 `owner_id` 的组合合法。
- 删除问题时附件会因 `question_id` 外键级联删除。
- 删除答案或复盘时，不会根据 `owner_type/owner_id` 自动删除附件。

## 4. 实体关系明细

| 主实体 | 从实体 | 基数 | 外键 | 删除行为 |
|---|---|---:|---|---|
| `questions` | `question_tags` | 1:N | `question_tags.question_id` | 级联删除 |
| `questions` | `answer_attempts` | 1:N | `answer_attempts.question_id` | 级联删除 |
| `questions` | `mistake_reviews` | 1:N | `mistake_reviews.question_id` | 级联删除 |
| `questions` | `attachments` | 1:N | `attachments.question_id` | 级联删除 |
| `answer_attempts` | `mistake_reviews` | 1:N，可选 | `mistake_reviews.answer_attempt_id` | 删除作答时置空 |

## 5. 索引

当前迁移创建了以下索引：

| 索引 | 表 | 字段 | 用途 |
|---|---|---|---|
| `idx_questions_updated_at` | `questions` | `updated_at DESC, id DESC` | 按更新时间分页和排序 |
| `idx_questions_type` | `questions` | `type` | 按题型筛选 |
| `idx_questions_archived` | `questions` | `is_archived` | 按归档状态筛选 |
| `idx_answers_question_id` | `answer_attempts` | `question_id, created_at DESC` | 查询某题的作答历史 |
| `idx_reviews_question_id` | `mistake_reviews` | `question_id, created_at DESC` | 查询某题的复盘历史 |
| `idx_attachments_owner` | `attachments` | `owner_type, owner_id` | 按多态拥有者查询附件 |

此外，以下约束会隐式提供唯一性索引：

- 各实体的主键。
- `question_tags(question_id, tag)` 复合主键。
- `attachments.stored_name` 唯一约束。

## 6. 时间与标识规范

从领域模型看，数据库时间字段使用 `TEXT` 存储，并在 Go 中映射为 `time.Time`。当前表中时间字段包括：

- `created_at`：创建时间。
- `updated_at`：更新时间。
- `schema_migrations.applied_at`：迁移应用时间。

业务实体 ID 使用 `TEXT`，具体 ID 生成策略由应用层负责，数据库不生成自增 ID。

## 7. 完整性与约束总结

### 数据库已保证

- 问题、作答、复盘和附件必须有 ID。
- 问题标题、正文和题型不能为空。
- 题型、难度、作答结果只能使用规定枚举值。
- 归档字段只能是 `0` 或 `1`。
- 文件大小不能小于零。
- 附件存储文件名全局唯一。
- 子记录引用的问题必须存在（前提是 SQLite 外键约束已启用）。
- 删除问题时相关子记录级联清理。

### 主要依赖应用层保证

- 标签内容不能为空、格式规范且是否大小写敏感。
- `source_url` 是否为合法 URL。
- 时间文本的格式和时区一致性。
- ID 的格式和唯一性生成策略。
- `owner_type/owner_id` 的合法性。
- 复盘关联作答与所属问题的一致性。
- `duration_ms` 是否非负。
- 附件 SHA-256 的格式和长度。
- `owner_type` 支持哪些类型。

## 8. 当前设计特点与边界

### 优点

- 核心问题与历史作答、复盘、附件分离，结构清晰。
- 标签采用关联表，避免将多个标签塞入单字段。
- 重要枚举由 `CHECK` 约束保护，减少非法状态。
- 删除问题时具备完整的级联清理行为。
- 复盘与作答是可选关联，支持没有具体作答对象的通用复盘。
- 附件使用摘要和唯一存储名，便于文件完整性检查和存储管理。

### 当前边界

- 没有独立用户表，作答记录没有用户维度。
- 没有独立标签表，标签无法记录描述、颜色、创建时间等元数据。
- 没有全文搜索虚拟表；问题正文和标题的文本搜索需要由应用查询实现。
- 没有单独的复习计划、复习次数或间隔重复字段。
- `owner_type/owner_id` 是多态关联，数据库无法提供完整的外键完整性。
- 附件只有元数据表，文件生命周期由文件系统存储层负责。
- 数据库脚本没有在表定义中显式声明 `PRAGMA foreign_keys = ON`；SQLite 连接初始化必须确保外键校验已启用，否则外键和级联行为不会生效。

## 9. 推荐查询路径

### 查询问题及其标签

```sql
SELECT q.*, qt.tag
FROM questions q
LEFT JOIN question_tags qt ON qt.question_id = q.id
WHERE q.id = ?;
```

### 查询问题的作答历史

```sql
SELECT *
FROM answer_attempts
WHERE question_id = ?
ORDER BY created_at DESC;
```

### 查询问题复盘及关联作答

```sql
SELECT mr.*, aa.result, aa.created_at AS answer_created_at
FROM mistake_reviews mr
LEFT JOIN answer_attempts aa
  ON aa.id = mr.answer_attempt_id
WHERE mr.question_id = ?
ORDER BY mr.created_at DESC;
```

### 查询问题完整详情

建议分别查询主表、标签、作答、复盘和附件，再由应用层组装为 `QuestionDetail`，而不是通过多表笛卡尔积一次性查询。

## 10. 相关代码位置

- 迁移脚本：`backend/internal/storage/migrations/0001_init.sql`
- 业务迁移：`backend/internal/storage/migrations/0002_question_memory.sql`
- Go 领域模型：`backend/internal/question/model.go`
- SQLite 问题仓储：`backend/internal/repository/sqlite/question.go`
- SQLite 作答仓储：`backend/internal/repository/sqlite/answer.go`
- SQLite 复盘仓储：`backend/internal/repository/sqlite/review.go`
- SQLite 附件仓储：`backend/internal/repository/sqlite/attachment.go`
- 数据库初始化与迁移：`backend/internal/storage/sqlite.go`、`backend/internal/storage/migrate.go`
