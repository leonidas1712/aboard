CREATE TABLE board_files (
 id TEXT PRIMARY KEY,
 board_id TEXT NOT NULL REFERENCES boards(id),
 name TEXT NOT NULL,
 record_json TEXT NOT NULL,
 UNIQUE(board_id, name)
);
