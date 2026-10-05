-- The members each message mentions, as its message.posted event records them: a read
-- model of the event's mentions. Messages posted before the server read mentions
-- mention no one.
ALTER TABLE messages ADD COLUMN mentions_json TEXT NOT NULL DEFAULT '[]';
