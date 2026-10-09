CREATE TABLE admin_allowances (
 id TEXT PRIMARY KEY,
 person_id TEXT NOT NULL UNIQUE REFERENCES humans(id),
 revision INTEGER NOT NULL DEFAULT 0,
 categories TEXT NOT NULL
);
CREATE TABLE admin_approvals (
 id TEXT PRIMARY KEY,
 person_id TEXT NOT NULL REFERENCES humans(id),
 agent_id TEXT NOT NULL REFERENCES members(id),
 parent_key_id TEXT NOT NULL REFERENCES access_keys(id),
 request_key TEXT,
 payload_hash TEXT NOT NULL,
 action TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','executed','declined','expired')),
 created_at TEXT NOT NULL,
 expires_at TEXT,
 decided_at TEXT,
 execution TEXT,
 UNIQUE(agent_id, request_key)
);
CREATE INDEX admin_approvals_person ON admin_approvals(person_id,created_at,id);
