CREATE TABLE shown_messages (
 harness TEXT NOT NULL,
 session_id TEXT NOT NULL,
 boot TEXT NOT NULL,
 server TEXT NOT NULL,
 member_id TEXT NOT NULL,
 generation INTEGER NOT NULL,
 board_id TEXT NOT NULL,
 message_id TEXT NOT NULL,
 seq INTEGER NOT NULL,
 PRIMARY KEY(harness,session_id,boot,server,member_id,generation,message_id)
);
