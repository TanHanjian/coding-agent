CREATE TABLE IF NOT EXISTS conversation_entry_nodes (
    id TEXT PRIMARY KEY NOT NULL,
    conversation_id TEXT NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    parent_id TEXT,
    kind TEXT NOT NULL CHECK (kind = 'message'),
    payload_version INTEGER NOT NULL CHECK (payload_version = 1),
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
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE IF NOT EXISTS conversation_entry_heads (
    conversation_id TEXT PRIMARY KEY NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    active_leaf_id TEXT,
    version INTEGER NOT NULL DEFAULT 0 CHECK (version >= 0),
    updated_at TEXT NOT NULL,
    FOREIGN KEY (conversation_id, active_leaf_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX IF NOT EXISTS idx_entry_children
    ON conversation_entry_nodes(conversation_id, parent_id, append_index);

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
