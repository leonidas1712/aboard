-- Server identity and secrets.
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE humans (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    token_digest TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL
);

-- The append-only log: the source of truth for everything below it.
CREATE TABLE events (
    board_id   TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    id         TEXT    NOT NULL UNIQUE,
    type       TEXT    NOT NULL,
    at         TEXT    NOT NULL,
    actor_json TEXT    NOT NULL,
    data_json  TEXT    NOT NULL,
    data_hash  TEXT    NOT NULL,
    prev_hash  TEXT    NOT NULL,
    hash       TEXT    NOT NULL,
    PRIMARY KEY (board_id, seq)
);

-- Read models, rebuilt from events.
CREATE TABLE boards (
    id          TEXT PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    template    TEXT,
    charter     TEXT    NOT NULL,
    roles_json  TEXT    NOT NULL,
    policy_json TEXT    NOT NULL,
    head_seq    INTEGER NOT NULL,
    head_hash   TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    created_by  TEXT    NOT NULL
);

CREATE TABLE members (
    id           TEXT PRIMARY KEY,
    board_id     TEXT    NOT NULL REFERENCES boards (id),
    name         TEXT    NOT NULL,
    kind         TEXT    NOT NULL CHECK (kind IN ('agent', 'human')),
    role         TEXT,
    human_id     TEXT    NOT NULL REFERENCES humans (id),
    owner        TEXT,
    harness      TEXT,
    token_digest TEXT UNIQUE,
    status       TEXT    NOT NULL,
    cursor       INTEGER NOT NULL DEFAULT 0,
    joined_at    TEXT    NOT NULL,
    UNIQUE (board_id, name)
);

CREATE TABLE join_codes (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards (id),
    code_digest TEXT NOT NULL UNIQUE,
    role        TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    created_by  TEXT NOT NULL REFERENCES members (id),
    revoked_at  TEXT
);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    board_id        TEXT    NOT NULL REFERENCES boards (id),
    seq             INTEGER NOT NULL,
    at              TEXT    NOT NULL,
    sender_id       TEXT    NOT NULL REFERENCES members (id),
    to_json         TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    reply_to        TEXT,
    urgent          INTEGER NOT NULL,
    expects_reply   INTEGER NOT NULL,
    redactions_json TEXT    NOT NULL,
    UNIQUE (board_id, seq)
);

-- Responses to writes sent with an Idempotency-Key, kept so a retry gets the same answer.
CREATE TABLE idempotency (
    scope        TEXT    NOT NULL,
    key          TEXT    NOT NULL,
    request_hash TEXT    NOT NULL,
    status       INTEGER NOT NULL,
    content_type TEXT    NOT NULL,
    body         BLOB    NOT NULL,
    created_at   TEXT    NOT NULL,
    PRIMARY KEY (scope, key)
);
