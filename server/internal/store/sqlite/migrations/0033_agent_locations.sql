CREATE TABLE agent_locations (
 member_id TEXT PRIMARY KEY REFERENCES members(id),
 machine TEXT NOT NULL,
 harness TEXT NOT NULL,
 session_id TEXT NOT NULL,
 folder TEXT NOT NULL,
 last_active TEXT NOT NULL
);
