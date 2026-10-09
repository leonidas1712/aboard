ALTER TABLE humans ADD COLUMN midturn_policy TEXT NOT NULL DEFAULT 'my-agents' CHECK (midturn_policy IN ('owner-only', 'my-agents'));
ALTER TABLE members ADD COLUMN midturn_policy TEXT CHECK (midturn_policy IN ('owner-only', 'my-agents'));

CREATE TABLE delivery_queue_reports (
 member_id TEXT PRIMARY KEY REFERENCES members(id),
 board_id TEXT NOT NULL REFERENCES boards(id),
 session TEXT NOT NULL,
 boot TEXT NOT NULL,
 credential_digest TEXT NOT NULL,
 epoch INTEGER NOT NULL CHECK(epoch > 0),
 revision INTEGER NOT NULL CHECK(revision >= 0),
 expires_at TEXT NOT NULL,
 messages TEXT NOT NULL
);
