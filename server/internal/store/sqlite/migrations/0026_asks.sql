ALTER TABLE messages ADD COLUMN ask_json TEXT NOT NULL DEFAULT 'null';
ALTER TABLE messages ADD COLUMN answer_json TEXT NOT NULL DEFAULT 'null';
CREATE INDEX messages_answers ON messages(json_extract(answer_json, '$.ask_id'), seq);
