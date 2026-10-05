-- Guests, and removing people from the server. A person's server role may now be
-- 'guest', for someone who came in through a guest code. A person removed from the
-- server keeps their row, with when and by whom, so their messages stay theirs; their
-- handle is free again at once, so names are unique only among the people still on the
-- server. SQLite can't change a CHECK or a UNIQUE column in place, so the humans table is
-- rebuilt; the migration runs with foreign keys checked once at the end, and every row
-- keeps its id.

CREATE TABLE people (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    display_name TEXT,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member', 'guest')),
    created_at   TEXT NOT NULL,
    removed_at   TEXT,
    removed_by   TEXT REFERENCES people (id)
);

INSERT INTO people (id, name, display_name, role, created_at)
SELECT id, name, display_name, role, created_at FROM humans ORDER BY rowid;

DROP TABLE humans;
ALTER TABLE people RENAME TO humans;

CREATE UNIQUE INDEX humans_current_name ON humans (name) WHERE removed_at IS NULL;

-- A join code is a pairing code, which only its maker's own sessions redeem, or a guest
-- code, which lets the one guest it names onto its board, once. Every code made before
-- is a pairing code.
ALTER TABLE join_codes ADD COLUMN kind TEXT NOT NULL DEFAULT 'pairing' CHECK (kind IN ('pairing', 'guest'));
ALTER TABLE join_codes ADD COLUMN guest TEXT;
ALTER TABLE join_codes ADD COLUMN used_at TEXT;
ALTER TABLE join_codes ADD COLUMN used_by TEXT REFERENCES members (id);
