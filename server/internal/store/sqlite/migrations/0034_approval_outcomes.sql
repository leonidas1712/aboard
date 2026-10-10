CREATE TABLE admin_approval_outcomes (
    approval_id TEXT PRIMARY KEY,
    invite_id TEXT NOT NULL UNIQUE,
    version INTEGER NOT NULL CHECK (version = 1),
    consumed_at TEXT
);
