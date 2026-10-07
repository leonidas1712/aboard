ALTER TABLE boards ADD COLUMN task_prefix TEXT;
CREATE TABLE task_prefixes (prefix TEXT PRIMARY KEY, board_id TEXT NOT NULL REFERENCES boards(id));
CREATE TABLE tasks (
 id TEXT PRIMARY KEY, board_id TEXT NOT NULL REFERENCES boards(id),
 number INTEGER NOT NULL, ref TEXT NOT NULL UNIQUE, title TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('open','in_progress','done','cancelled')),
 about_json TEXT, stands_json TEXT, owner_id TEXT REFERENCES members(id),
 helpers_json TEXT NOT NULL, opened_by TEXT NOT NULL REFERENCES members(id),
 opened_at TEXT NOT NULL, updated_at TEXT NOT NULL, closed_at TEXT, closed_note TEXT,
 UNIQUE(board_id, number)
);
ALTER TABLE members ADD COLUMN current_task_id TEXT REFERENCES tasks(id);
ALTER TABLE messages ADD COLUMN about_json TEXT NOT NULL DEFAULT '[]';
