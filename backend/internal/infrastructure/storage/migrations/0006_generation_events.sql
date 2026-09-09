-- 生成事件是恢复 SSE 的持久化日志；messages.content 仍保存当前可见全文。
CREATE TABLE generation_events (
    assistant_message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    kind TEXT NOT NULL CHECK (kind IN ('text', 'finish', 'error')),
    text TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (assistant_message_id, sequence)
);

CREATE INDEX idx_generation_events_replay
    ON generation_events(assistant_message_id, sequence ASC);
