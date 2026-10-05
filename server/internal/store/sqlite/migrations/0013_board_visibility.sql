-- Boards are open (every person on the server sees them and may join) or private (only
-- the people on them). Every board made before this is open, so a local server reads
-- exactly as it did. Its creator is already its admin, which is its owner, so every
-- board keeps an owner. A person who leaves or is removed keeps their member row, with
-- status 'left' or 'removed', so their messages stay theirs; nothing to change for
-- existing rows, which are all 'active'.
ALTER TABLE boards ADD COLUMN visibility TEXT NOT NULL DEFAULT 'open' CHECK (visibility IN ('open', 'private'));
