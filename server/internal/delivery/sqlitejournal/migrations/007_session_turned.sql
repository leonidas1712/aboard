-- Whether the session has run a turn: a harness can resume a session only after one,
-- since only then does it save the conversation (Claude Code).

ALTER TABLE sessions ADD COLUMN turned INTEGER NOT NULL DEFAULT 0;
