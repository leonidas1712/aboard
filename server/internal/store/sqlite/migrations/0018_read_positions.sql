-- Recipients are recorded only for new message.posted events. Historical roles and
-- names cannot reconstruct whom an old message addressed. NULL leaves them unknown.
ALTER TABLE messages ADD COLUMN recipients_json TEXT;

-- A person's read position on a board was never moved before, so it stood at 0 and every
-- message would count as unread. Each person starts where they are now: at the board's
-- head, as a person joining a board does.
UPDATE members SET cursor = max(cursor, (SELECT head_seq FROM boards WHERE boards.id = members.board_id))
WHERE kind = 'human';
