-- Browser sessions get an id, so a person can list them and end one at a time without
-- the secret, and say how they started: from aboard open's one-time code, or from an
-- access key pasted on the login page. Sessions made before this get an id from their
-- digest (hex is a subset of the id alphabet) and count as started from aboard open,
-- the only way there was.
ALTER TABLE browser_logins ADD COLUMN id TEXT;
ALTER TABLE browser_logins ADD COLUMN started_with TEXT NOT NULL DEFAULT 'login_code';
UPDATE browser_logins SET id = 'ses_' || upper(substr(token_digest, 1, 26));

CREATE UNIQUE INDEX browser_logins_by_id ON browser_logins (id);
