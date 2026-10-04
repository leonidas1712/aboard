-- The delivery modes focused and all, with auto, all's earlier name, still kept as
-- a daemon from an older build reports it. SQLite can't change a column's CHECK, so the
-- column is replaced by one with the new list.
ALTER TABLE members ADD COLUMN delivery_mode TEXT CHECK (delivery_mode IN ('focused', 'all', 'humans', 'off', 'auto'));
UPDATE members SET delivery_mode = delivery;
ALTER TABLE members DROP COLUMN delivery;
ALTER TABLE members RENAME COLUMN delivery_mode TO delivery;
