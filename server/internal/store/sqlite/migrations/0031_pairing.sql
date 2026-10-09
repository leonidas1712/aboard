CREATE TABLE pairing_requests (
 id TEXT PRIMARY KEY,
 invite_id TEXT NOT NULL DEFAULT '',
 inviter_id TEXT NOT NULL REFERENCES humans(id),
 recipient_id TEXT REFERENCES humans(id),
 created_at TEXT NOT NULL,
 data BLOB NOT NULL
);
CREATE UNIQUE INDEX pairing_invite ON pairing_requests(invite_id) WHERE invite_id != '';
CREATE INDEX pairing_inviter ON pairing_requests(inviter_id, created_at);
CREATE INDEX pairing_recipient ON pairing_requests(recipient_id, created_at);
CREATE TABLE pairing_credentials (
 id TEXT PRIMARY KEY,
 request_id TEXT NOT NULL REFERENCES pairing_requests(id),
 digest TEXT NOT NULL UNIQUE,
 data BLOB NOT NULL
);
CREATE INDEX pairing_credential_request ON pairing_credentials(request_id);
