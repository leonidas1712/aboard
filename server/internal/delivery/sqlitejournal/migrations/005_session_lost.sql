-- The agent another session resumed while this one held it, so the session can say so
-- when it starts again instead of taking the agent back. Empty when there is none.

ALTER TABLE sessions ADD COLUMN lost_server TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN lost_board TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN lost_agent TEXT NOT NULL DEFAULT '';
