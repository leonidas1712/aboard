CREATE TABLE admin_approval_outcomes (
    approval_id TEXT PRIMARY KEY,
    invite_id TEXT NOT NULL UNIQUE,
    version INTEGER NOT NULL CHECK (version = 1),
    capsule BLOB,
    expires_at TEXT NOT NULL,
    winning_key_hash TEXT,
    consumed_at TEXT
);
