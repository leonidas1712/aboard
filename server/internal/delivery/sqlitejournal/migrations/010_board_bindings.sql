DROP INDEX bindings_one_per_session;
CREATE UNIQUE INDEX bindings_one_per_board ON bindings (harness, session_id, server, board);
