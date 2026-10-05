-- When each access key was last used, by itself or through a browser login or agent token
-- it started, recorded at most once a minute; and, for a key that expires only once
-- unused, how far each use moves its expiry. Counting what a key started looks keys up
-- in members and browser logins.
ALTER TABLE access_keys ADD COLUMN last_used_at TEXT;
ALTER TABLE access_keys ADD COLUMN idle_seconds INTEGER;

CREATE INDEX members_by_key ON members (key_id);
CREATE INDEX browser_logins_by_key ON browser_logins (key_id);
