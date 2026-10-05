-- People and their access keys. A person (still a row of humans) gets a server role and
-- an optional display name; their login becomes their first access key, kept as a
-- keyed digest like every other secret. Agent tokens and browser logins record the key
-- they came from, so they stop working when it does. The humans table is rebuilt to
-- drop its token column, which SQLite can't drop from a UNIQUE column in place; the
-- migration runs with foreign keys checked once at the end.

CREATE TABLE access_keys (
    id         TEXT PRIMARY KEY,
    human_id   TEXT NOT NULL REFERENCES humans (id),
    name       TEXT NOT NULL,
    digest     TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    expires_at TEXT,
    revoked_at TEXT
);

CREATE INDEX access_keys_by_human ON access_keys (human_id);

-- Each existing login becomes a key with the person's id's time and randomness, so the
-- id is as unique as the person's. Its name is filled in with the machine's name when
-- the server next starts.
INSERT INTO access_keys (id, human_id, name, digest, created_at)
SELECT 'key_' || substr(id, 5), id, '', token_digest, created_at FROM humans;

CREATE TABLE people (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    display_name TEXT,
    role         TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_at   TEXT NOT NULL
);

-- The first person on a server is its admin. A local server has only ever had one.
INSERT INTO people (id, name, display_name, role, created_at)
SELECT id, name, NULL, CASE WHEN rowid = (SELECT min(rowid) FROM humans) THEN 'admin' ELSE 'member' END, created_at
FROM humans;

DROP TABLE humans;
ALTER TABLE people RENAME TO humans;

ALTER TABLE members ADD COLUMN key_id TEXT REFERENCES access_keys (id);
UPDATE members SET key_id = 'key_' || substr(human_id, 5) WHERE kind = 'agent';

ALTER TABLE browser_logins ADD COLUMN key_id TEXT REFERENCES access_keys (id);
UPDATE browser_logins SET key_id = 'key_' || substr(human_id, 5);

-- Server invites: each makes one new person, a member, once, before it expires. Only
-- the keyed digest of the invite is kept.
CREATE TABLE server_invites (
    id         TEXT PRIMARY KEY,
    digest     TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL REFERENCES humans (id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    used_by    TEXT REFERENCES humans (id)
);
