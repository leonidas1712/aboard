ALTER TABLE humans ADD COLUMN midturn_policy TEXT NOT NULL DEFAULT 'my-agents' CHECK (midturn_policy IN ('owner-only', 'my-agents'));
ALTER TABLE members ADD COLUMN midturn_policy TEXT CHECK (midturn_policy IN ('owner-only', 'my-agents'));
