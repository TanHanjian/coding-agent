CREATE TABLE IF NOT EXISTS conversation_context_summaries (
    conversation_id TEXT PRIMARY KEY NOT NULL
        REFERENCES conversations(id) ON DELETE CASCADE,
    schema_version INTEGER NOT NULL,
    covered_sequence INTEGER NOT NULL CHECK (covered_sequence >= 0),
    summary_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
