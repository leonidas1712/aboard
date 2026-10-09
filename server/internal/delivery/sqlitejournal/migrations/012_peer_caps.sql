ALTER TABLE sessions ADD COLUMN peer_turn_active INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN peer_turn INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN busy_at TEXT NOT NULL DEFAULT '';
CREATE TABLE peer_caps (
  harness TEXT NOT NULL,
  session_id TEXT NOT NULL,
  turn_id INTEGER NOT NULL,
  sender_server TEXT NOT NULL,
  sender_member_id TEXT NOT NULL,
  handoff_id TEXT NOT NULL,
  PRIMARY KEY (harness, session_id, turn_id, sender_server, sender_member_id)
);
