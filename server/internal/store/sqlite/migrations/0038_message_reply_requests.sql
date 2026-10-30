-- Replies waiting compares a request only with later requests from the same sender
-- in the same thread. Avoid scanning the remaining board history for every request.
CREATE INDEX IF NOT EXISTS messages_reply_requests
ON messages (board_id, sender_id, COALESCE(thread_root, id), seq)
WHERE expects_reply AND ask_json = 'null';
