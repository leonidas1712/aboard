-- The delivery journal: sessions, which session each agent is bound to, and every
-- delivery with the sequence numbers of its messages. It never holds tokens or message
-- bodies.

CREATE TABLE sessions (
    harness    TEXT NOT NULL,
    session_id TEXT NOT NULL,
    boot       TEXT NOT NULL,
    open       INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (harness, session_id)
);

CREATE TABLE bindings (
    server     TEXT NOT NULL,
    board      TEXT NOT NULL,
    agent      TEXT NOT NULL,
    harness    TEXT NOT NULL,
    session_id TEXT NOT NULL,
    bound_at   TEXT NOT NULL,
    PRIMARY KEY (server, board, agent)
);

CREATE TABLE deliveries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    server     TEXT NOT NULL,
    board      TEXT NOT NULL,
    agent      TEXT NOT NULL,
    harness    TEXT NOT NULL,
    session_id TEXT NOT NULL,
    boot       TEXT NOT NULL,
    state      TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',
    retry_at   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX deliveries_by_state ON deliveries (state);

CREATE TABLE delivery_messages (
    delivery_id INTEGER NOT NULL REFERENCES deliveries (id),
    seq         INTEGER NOT NULL,
    PRIMARY KEY (delivery_id, seq)
);
