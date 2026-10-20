-- Shared by runtime schema creation and sqlc. This is the current schema,
-- not a migration or a second independently maintained schema snapshot.
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1,
    strategy TEXT NOT NULL DEFAULT 'round-robin',
    max_retries INTEGER NOT NULL DEFAULT 3,
    retry_interval INTEGER NOT NULL DEFAULT 1000,
    max_concurrency INTEGER NOT NULL DEFAULT 0,
    daily_limit_max_requests INTEGER NOT NULL DEFAULT 0,
    daily_limit_max_tokens INTEGER NOT NULL DEFAULT 0,
    type TEXT NOT NULL DEFAULT 'llm',
    max_tokens INTEGER NOT NULL DEFAULT 0,
    vision_capable INTEGER NOT NULL DEFAULT 0,
    tools_capable INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_group_models (
    group_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    source_id TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, model_id, source_id),
    FOREIGN KEY (group_id) REFERENCES model_groups(id) ON DELETE CASCADE
);
