-- The harness process each session runs in, so a session whose harness died without
-- its end hook can be closed. Zero means the process isn't known.

ALTER TABLE sessions ADD COLUMN pid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN pid_start INTEGER NOT NULL DEFAULT 0;
