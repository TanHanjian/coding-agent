-- 扩展 conversation_entry_nodes 支持 compaction kind
-- compaction entry 用于保存上下文摘要，作为不可变树节点追加

-- 1. 允许 kind = 'compaction'
DROP TABLE IF EXISTS conversation_entry_nodes_new;
CREATE TABLE conversation_entry_nodes_new (
    id TEXT PRIMARY KEY NOT NULL,
    conversation_id TEXT NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    parent_id TEXT,
    kind TEXT NOT NULL CHECK (kind IN ('message', 'compaction')),
    payload_version INTEGER NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    append_index INTEGER NOT NULL CHECK (append_index > 0),
    depth INTEGER NOT NULL CHECK (depth >= 0),
    created_at TEXT NOT NULL,
    UNIQUE (conversation_id, id),
    UNIQUE (conversation_id, append_index),
    CHECK (parent_id IS NULL OR parent_id <> id),
    CHECK ((parent_id IS NULL AND depth = 0)
        OR (parent_id IS NOT NULL AND depth > 0)),
    FOREIGN KEY (conversation_id, parent_id)
        REFERENCES conversation_entry_nodes_new(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

-- 2. 迁移已有数据
INSERT INTO conversation_entry_nodes_new
SELECT id, conversation_id, parent_id, kind, payload_version, payload_json,
       append_index, depth, created_at
FROM conversation_entry_nodes;

-- 3. 删除旧表，重命名新表
DROP TABLE conversation_entry_nodes;
ALTER TABLE conversation_entry_nodes_new RENAME TO conversation_entry_nodes;

-- 4. 重建索引
CREATE INDEX IF NOT EXISTS idx_entry_children
    ON conversation_entry_nodes(conversation_id, parent_id, append_index);

-- 5. 重建触发器
CREATE TRIGGER IF NOT EXISTS entry_nodes_no_update
BEFORE UPDATE ON conversation_entry_nodes
BEGIN
    SELECT RAISE(ABORT, 'entry_immutable');
END;

CREATE TRIGGER IF NOT EXISTS entry_nodes_no_individual_delete
BEFORE DELETE ON conversation_entry_nodes
WHEN EXISTS (
    SELECT 1 FROM conversations WHERE id = OLD.conversation_id
)
BEGIN
    SELECT RAISE(ABORT, 'entry_immutable');
END;

CREATE TRIGGER IF NOT EXISTS entry_nodes_no_replace
BEFORE INSERT ON conversation_entry_nodes
WHEN EXISTS (
    SELECT 1 FROM conversation_entry_nodes
    WHERE id = NEW.id OR (conversation_id = NEW.conversation_id AND append_index = NEW.append_index)
)
BEGIN
    SELECT RAISE(ABORT, 'entry_immutable');
END;
