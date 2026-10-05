-- Unverified entries retain their board and name until their own credential proves
-- their member id. Resolved entries use the server and member id as their key.
ALTER TABLE sessions ADD COLUMN lost_member_id TEXT NOT NULL DEFAULT '';
ALTER TABLE deliveries ADD COLUMN member_id TEXT NOT NULL DEFAULT '';

ALTER TABLE bindings RENAME TO old_bindings;
DROP INDEX bindings_one_per_session;
CREATE TABLE bindings (
    server TEXT NOT NULL,
    board TEXT NOT NULL,
    agent TEXT NOT NULL,
    member_id TEXT NOT NULL DEFAULT '',
    harness TEXT NOT NULL,
    session_id TEXT NOT NULL,
    bound_at TEXT NOT NULL
);
INSERT INTO bindings (server, board, agent, harness, session_id, bound_at)
SELECT server, board, agent, harness, session_id, bound_at FROM old_bindings;
DROP TABLE old_bindings;
CREATE UNIQUE INDEX bindings_member ON bindings (server, member_id) WHERE member_id <> '';
CREATE UNIQUE INDEX bindings_legacy ON bindings (server, board, agent) WHERE member_id = '';
CREATE UNIQUE INDEX bindings_one_per_session ON bindings (harness, session_id);

ALTER TABLE modes RENAME TO old_modes;
CREATE TABLE modes (
    server TEXT NOT NULL,
    board TEXT NOT NULL,
    agent TEXT NOT NULL,
    member_id TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL
);
INSERT INTO modes (server, board, agent, mode)
SELECT server, board, agent, mode FROM old_modes;
DROP TABLE old_modes;
CREATE UNIQUE INDEX modes_member ON modes (server, member_id) WHERE member_id <> '';
CREATE UNIQUE INDEX modes_legacy ON modes (server, board, agent) WHERE member_id = '';
