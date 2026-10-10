CREATE TABLE approval_watches (
 server TEXT NOT NULL,
 approval_id TEXT NOT NULL,
 member_id TEXT NOT NULL,
 harness TEXT NOT NULL,
 session_id TEXT NOT NULL,
 boot TEXT NOT NULL,
 generation INTEGER NOT NULL,
 state TEXT NOT NULL,
 invalidated INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(server,approval_id)
);

CREATE INDEX approval_watches_session ON approval_watches(harness,session_id,invalidated);
