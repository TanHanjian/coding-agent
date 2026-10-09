CREATE UNIQUE INDEX idx_messages_conversation_id_reference
    ON messages(conversation_id, id);

CREATE TABLE agent_runs (
    id TEXT PRIMARY KEY NOT NULL,
    conversation_id TEXT NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    client_message_id TEXT NOT NULL CHECK (length(client_message_id) > 0),
    user_message_id TEXT NOT NULL UNIQUE,
    assistant_message_id TEXT NOT NULL UNIQUE,

    base_entry_id TEXT,
    base_head_version INTEGER NOT NULL CHECK (base_head_version >= 0),
    user_entry_id TEXT NOT NULL,
    last_entry_id TEXT NOT NULL,
    last_closed_entry_id TEXT NOT NULL,
    final_entry_id TEXT,
    head_version INTEGER NOT NULL CHECK (head_version >= 0),

    status TEXT NOT NULL CHECK (status IN (
        'pending', 'running', 'waiting_tool', 'completed', 'failed', 'cancelled'
    )),
    error_code TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    UNIQUE (conversation_id, client_message_id),
    CHECK (
        (status IN ('pending', 'running', 'waiting_tool') AND finished_at IS NULL)
        OR (status IN ('completed', 'failed', 'cancelled') AND finished_at IS NOT NULL)
    ),
    CHECK (
        (status = 'completed' AND final_entry_id IS NOT NULL AND error_code = '')
        OR (status <> 'completed' AND final_entry_id IS NULL)
    ),
    FOREIGN KEY (conversation_id, user_message_id)
        REFERENCES messages(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, assistant_message_id)
        REFERENCES messages(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, base_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, user_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, last_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, last_closed_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (conversation_id, final_entry_id)
        REFERENCES conversation_entry_nodes(conversation_id, id)
        ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX idx_agent_runs_one_active
    ON agent_runs(conversation_id)
    WHERE status IN ('pending', 'running', 'waiting_tool');
