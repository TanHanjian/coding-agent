CREATE TABLE IF NOT EXISTS questions (
    id TEXT PRIMARY KEY NOT NULL,
    title TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('algorithm', 'knowledge', 'system_design', 'behavioral', 'other')),
    body_markdown TEXT NOT NULL,
    difficulty TEXT CHECK (difficulty IS NULL OR difficulty IN ('easy', 'medium', 'hard')),
    source_name TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    is_archived INTEGER NOT NULL DEFAULT 0 CHECK (is_archived IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS question_tags (
    question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    PRIMARY KEY (question_id, tag)
);

CREATE TABLE IF NOT EXISTS answer_attempts (
    id TEXT PRIMARY KEY NOT NULL,
    question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    body_markdown TEXT NOT NULL DEFAULT '',
    code TEXT NOT NULL DEFAULT '',
    code_language TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL CHECK (result IN ('skipped', 'incorrect', 'partial', 'correct')),
    duration_ms INTEGER,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mistake_reviews (
    id TEXT PRIMARY KEY NOT NULL,
    question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    answer_attempt_id TEXT REFERENCES answer_attempts(id) ON DELETE SET NULL,
    mistake_category TEXT NOT NULL DEFAULT '',
    review_markdown TEXT NOT NULL DEFAULT '',
    correction_markdown TEXT NOT NULL DEFAULT '',
    key_conclusions TEXT NOT NULL DEFAULT '',
    ai_content_markdown TEXT NOT NULL DEFAULT '',
    ai_source TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY NOT NULL,
    question_id TEXT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    owner_type TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    original_name TEXT NOT NULL,
    stored_name TEXT NOT NULL UNIQUE,
    mime_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_questions_updated_at ON questions(updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_questions_type ON questions(type);
CREATE INDEX IF NOT EXISTS idx_questions_archived ON questions(is_archived);
CREATE INDEX IF NOT EXISTS idx_answers_question_id ON answer_attempts(question_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reviews_question_id ON mistake_reviews(question_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_attachments_owner ON attachments(owner_type, owner_id);
