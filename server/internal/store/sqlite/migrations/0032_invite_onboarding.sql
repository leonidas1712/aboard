ALTER TABLE server_invites ADD COLUMN boards TEXT NOT NULL DEFAULT '[]';
CREATE TABLE onboarding_receipts (
 key_id TEXT PRIMARY KEY REFERENCES access_keys(id),
 person_id TEXT NOT NULL REFERENCES humans(id),
 invite_id TEXT NOT NULL REFERENCES server_invites(id),
 server_id TEXT NOT NULL,
 handle TEXT NOT NULL,
 boards TEXT NOT NULL
);
ALTER TABLE server_invites ADD COLUMN parent_key_id TEXT REFERENCES access_keys(id);
ALTER TABLE server_invites ADD COLUMN issuing_agent_id TEXT REFERENCES members(id);
ALTER TABLE server_invites ADD COLUMN authorization TEXT;
ALTER TABLE server_invites ADD COLUMN revoked_at TEXT;
ALTER TABLE onboarding_receipts ADD COLUMN pairing_request_id TEXT;
