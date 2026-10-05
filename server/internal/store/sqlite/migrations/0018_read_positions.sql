-- Whom each message was addressed to when it was posted, as a JSON array of member ids:
-- each member named with @name, and each member who held a role:R target's role then,
-- never the sender. Null for a message to all, which has no receipts. A read model of the
-- message.posted event's recipients. Messages posted before it was recorded get the
-- members they named, and the members with the role who had joined by then.
ALTER TABLE messages ADD COLUMN recipients_json TEXT;

UPDATE messages SET recipients_json = (
    SELECT json_group_array(mb.id) FROM members mb
    WHERE mb.board_id = messages.board_id AND mb.id <> messages.sender_id AND (
        EXISTS (SELECT 1 FROM json_each(messages.to_json) t WHERE t.value = '@' || mb.name)
        OR (mb.role IS NOT NULL AND mb.joined_at <= messages.at
            AND EXISTS (SELECT 1 FROM json_each(messages.to_json) t WHERE t.value = 'role:' || mb.role))))
WHERE NOT EXISTS (SELECT 1 FROM json_each(messages.to_json) WHERE value = 'all');

-- A person's read position on a board was never moved before, so it stood at 0 and every
-- message would count as unread. Each person starts where they are now: at the board's
-- head, as a person joining a board does.
UPDATE members SET cursor = max(cursor, (SELECT head_seq FROM boards WHERE boards.id = members.board_id))
WHERE kind = 'human';
