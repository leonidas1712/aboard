ALTER TABLE boards ADD COLUMN agents_add_people INTEGER NOT NULL DEFAULT 1 CHECK (agents_add_people IN (0, 1));
UPDATE boards SET agents_add_people = 0 WHERE visibility = 'private';
CREATE TABLE delegated_creations (
 delegation_id TEXT NOT NULL REFERENCES delegations(id),
 key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 created_at TEXT NOT NULL,
 result_json TEXT NOT NULL,
 PRIMARY KEY (delegation_id, key)
);
