CREATE TABLE board_files (
 id TEXT PRIMARY KEY,
 board_id TEXT NOT NULL REFERENCES boards(id),
 name TEXT NOT NULL,
 record_json TEXT NOT NULL,
 removed INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX board_files_live_name ON board_files(board_id, name) WHERE removed = 0;
ALTER TABLE messages ADD COLUMN files_json TEXT NOT NULL DEFAULT '[]';
