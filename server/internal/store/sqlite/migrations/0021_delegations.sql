-- A machine's delegation: what the delivery daemon holds to list its person's boards and
-- give the sessions it vouches for a seat, made with one of the person's access keys and
-- kept by the keyed digest of its token. It ends with its key, with its person's removal
-- from the server, or when the same key makes another with the same name (ended_at).
CREATE TABLE delegations (
    id         TEXT PRIMARY KEY,
    key_id     TEXT NOT NULL REFERENCES access_keys (id),
    human_id   TEXT NOT NULL REFERENCES humans (id),
    name       TEXT NOT NULL,
    digest     TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    ended_at   TEXT
);

CREATE INDEX delegations_by_key ON delegations (key_id, name);

-- The harness session a seat was made for (`<harness>:<id>`), so a later delegated join
-- from the same session finds it. It is looked up only with the person's id and the
-- board's, never shown and never written to the record.
ALTER TABLE members ADD COLUMN session TEXT;

CREATE INDEX members_by_session ON members (board_id, human_id, session) WHERE session IS NOT NULL;

-- When an agent was removed, and by whom: 'person' (its own person), 'board_owner' or
-- 'admin'. Agents removed before this was kept have neither.
ALTER TABLE members ADD COLUMN removed_at TEXT;
ALTER TABLE members ADD COLUMN removed_by TEXT CHECK (removed_by IN ('person', 'board_owner', 'admin'));
