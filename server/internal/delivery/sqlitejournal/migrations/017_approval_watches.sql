CREATE TABLE approval_watches (
 server TEXT NOT NULL,
 approval_id TEXT NOT NULL,
 member_id TEXT NOT NULL,
 harness TEXT NOT NULL,
 session_id TEXT NOT NULL,
 boot TEXT NOT NULL,
 generation INTEGER NOT NULL,
 state TEXT NOT NULL,
 PRIMARY KEY(server,approval_id)
);
