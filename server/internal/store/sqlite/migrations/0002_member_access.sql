-- What each person may change on a board: the creator is its admin, everyone else a
-- member. Agents have no access level.
ALTER TABLE members ADD COLUMN access TEXT CHECK (access IN ('admin', 'member'));

UPDATE members SET access = CASE WHEN id IN (SELECT created_by FROM boards) THEN 'admin' ELSE 'member' END
WHERE kind = 'human';
