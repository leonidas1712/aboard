-- What each agent's session is doing, as its owner's delivery daemon last reported it.
-- Bookkeeping like the read position: never an event. presence_since is when the
-- presence began, presence_at when it was last reported; an unrenewed presence reads as
-- no_session.
ALTER TABLE members ADD COLUMN presence TEXT CHECK (presence IN ('working', 'idle', 'waiting', 'no_session'));
ALTER TABLE members ADD COLUMN presence_since TEXT;
ALTER TABLE members ADD COLUMN presence_at TEXT;
