-- New machines asking for an access key. A request is approved or refused with its short
-- code by a person signed in elsewhere, and the machine collects its key with a long
-- secret only it holds. Only keyed digests of the code and the secret are kept. Requests
-- last minutes, and ended ones are deleted when the next request is made, so the short
-- code is unique among the requests kept.
CREATE TABLE IF NOT EXISTS machine_requests (
    id             TEXT PRIMARY KEY,
    code_digest    TEXT NOT NULL UNIQUE,
    secret_digest  TEXT NOT NULL UNIQUE,
    label          TEXT NOT NULL,
    requested_from TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'approved', 'refused', 'collected')),
    decided_by     TEXT REFERENCES humans (id),
    decided_key    TEXT REFERENCES access_keys (id),
    decided_at     TEXT,
    key_id         TEXT REFERENCES access_keys (id),
    polls          INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS machine_requests_by_expiry ON machine_requests (expires_at);
