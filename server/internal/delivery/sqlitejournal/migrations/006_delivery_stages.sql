-- How far each delivery got in its session: when the harness accepted it (a waiting hook
-- took it, the harness's queue took it, or its extension added it), when the session's
-- next turn started after that, and whether it stalled: handed to an idle session that
-- started no turn in time. An empty time means not yet.

ALTER TABLE deliveries ADD COLUMN accepted_at TEXT NOT NULL DEFAULT '';
ALTER TABLE deliveries ADD COLUMN turn_started_at TEXT NOT NULL DEFAULT '';
ALTER TABLE deliveries ADD COLUMN stalled INTEGER NOT NULL DEFAULT 0;
