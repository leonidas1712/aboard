CREATE TABLE durable_notices (
 id TEXT PRIMARY KEY,
 server TEXT NOT NULL,
 member_id TEXT NOT NULL,
 state TEXT NOT NULL,
 handed INTEGER NOT NULL DEFAULT 0,
 cancelled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX durable_notices_seat ON durable_notices(server,member_id,handed,cancelled);
