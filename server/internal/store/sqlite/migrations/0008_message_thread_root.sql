-- The first message of each reply's thread, kept as replies are inserted, so a thread
-- can be read and counted without following reply_to message by message. A read model
-- like message_count: it follows from each message's reply_to in the event log. Null
-- for a message that replies to nothing, which starts its own thread.
ALTER TABLE messages ADD COLUMN thread_root TEXT REFERENCES messages (id);

WITH RECURSIVE chain (id, root) AS (
    SELECT id, id FROM messages WHERE reply_to IS NULL
    UNION ALL
    SELECT m.id, chain.root FROM messages m JOIN chain ON m.reply_to = chain.id
)
UPDATE messages SET thread_root = (SELECT root FROM chain WHERE chain.id = messages.id)
WHERE reply_to IS NOT NULL;

CREATE INDEX messages_thread_root ON messages (thread_root, seq);
