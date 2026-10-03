-- Browser logins from aboard open, kept so they outlive a restart of the server. Only the
-- keyed digest of each browser token is stored, never the token. Bookkeeping like read
-- cursors: never an event. Expired rows are deleted when the next login is made.
CREATE TABLE browser_logins (
    token_digest TEXT PRIMARY KEY,
    human_id     TEXT NOT NULL REFERENCES humans (id),
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

CREATE INDEX browser_logins_by_human ON browser_logins (human_id);
