-- How many messages each board holds and when the newest came, kept as messages are
-- inserted, so a list of boards needn't read every message.
ALTER TABLE boards ADD COLUMN message_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE boards ADD COLUMN last_message_at TEXT;

UPDATE boards SET
    message_count = (SELECT count(*) FROM messages m WHERE m.board_id = boards.id),
    last_message_at = (SELECT max(m.at) FROM messages m WHERE m.board_id = boards.id);
