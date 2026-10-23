-- Board badges check whether each question has a reply from its recipient.
-- Keep that lookup bounded as message history grows.
CREATE INDEX IF NOT EXISTS messages_reply_sender ON messages (reply_to, sender_id);
